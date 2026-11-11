package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5"
)

const (
	numberGameAttempts = 5
	numberGameRewardXP int64 = 10
)

var (
	errGameNotFound = errors.New("game not found")
	errGameFinished = errors.New("game already finished")
	gameIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type NumberGameResponse struct {
	GameID       string `json:"game_id,omitempty"`
	Status       string `json:"status"`
	AttemptsLeft int    `json:"attempts_left"`
	Result       string `json:"result,omitempty"`
	XPAwarded    int64  `json:"xp_awarded"`
	SecretNumber *int   `json:"secret_number,omitempty"`
}

func randomGameNumber() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(20))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()) + 1, nil
}

func compareGuess(secret, guess int) string {
	switch {
	case guess < secret:
		return "higher"
	case guess > secret:
		return "lower"
	default:
		return "correct"
	}
}

// StartNumberGame returns the existing active game, or creates one.
// Locking the user's row serializes concurrent starts for the same account.
func (r *Repository) StartNumberGame(ctx context.Context, userID string) (NumberGameResponse, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return NumberGameResponse{}, err
	}
	defer tx.Rollback(ctx)

	var lockedUser string
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedUser); err != nil {
		return NumberGameResponse{}, err
	}

	var game NumberGameResponse
	err = tx.QueryRow(ctx, `
		SELECT id, attempts_left, status
		FROM game_sessions
		WHERE user_id=$1 AND status='active'
		LIMIT 1
	`, userID).Scan(&game.GameID, &game.AttemptsLeft, &game.Status)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return NumberGameResponse{}, err
		}
		return game, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NumberGameResponse{}, err
	}

	secret, err := randomGameNumber()
	if err != nil {
		return NumberGameResponse{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO game_sessions(user_id, secret_number)
		VALUES ($1, $2)
		RETURNING id, attempts_left, status
	`, userID, secret).Scan(&game.GameID, &game.AttemptsLeft, &game.Status)
	if err != nil {
		return NumberGameResponse{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return NumberGameResponse{}, err
	}
	return game, nil
}

// CurrentNumberGame restores a player's latest game after a page refresh.
// The secret is only included once the game has finished.
func (r *Repository) CurrentNumberGame(ctx context.Context, userID string) (NumberGameResponse, error) {
	var game NumberGameResponse
	var secret int
	err := r.db.QueryRow(ctx, `
		SELECT id, secret_number, attempts_left, status
		FROM game_sessions
		WHERE user_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID).Scan(&game.GameID, &secret, &game.AttemptsLeft, &game.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return NumberGameResponse{Status: "none"}, nil
	}
	if err != nil {
		return NumberGameResponse{}, err
	}
	if game.Status != "active" {
		game.SecretNumber = &secret
	}
	return game, nil
}

// GuessNumberGame locks the session so each attempt is counted exactly once.
// Finishing the game and granting daily XP are one database transaction.
func (r *Repository) GuessNumberGame(ctx context.Context, userID, gameID string, guess int) (NumberGameResponse, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return NumberGameResponse{}, err
	}
	defer tx.Rollback(ctx)

	var secret int
	var game NumberGameResponse
	err = tx.QueryRow(ctx, `
		SELECT secret_number, attempts_left, status
		FROM game_sessions
		WHERE id=$1 AND user_id=$2
		FOR UPDATE
	`, gameID, userID).Scan(&secret, &game.AttemptsLeft, &game.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return NumberGameResponse{}, errGameNotFound
	}
	if err != nil {
		return NumberGameResponse{}, err
	}
	if game.Status != "active" {
		return NumberGameResponse{}, errGameFinished
	}

	game.GameID = gameID
	game.Result = compareGuess(secret, guess)
	game.AttemptsLeft--
	switch {
	case game.Result == "correct":
		game.Status = "won"
		game.SecretNumber = &secret
	case game.AttemptsLeft == 0:
		game.Status = "lost"
		game.SecretNumber = &secret
	default:
		game.Status = "active"
	}

	_, err = tx.Exec(ctx, `
		UPDATE game_sessions
		SET attempts_left=$1, status=$2,
		    finished_at=CASE WHEN $2='active' THEN NULL ELSE NOW() END
		WHERE id=$3 AND user_id=$4
	`, game.AttemptsLeft, game.Status, gameID, userID)
	if err != nil {
		return NumberGameResponse{}, err
	}

	if game.Status == "won" {
		var awarded int64
		err = tx.QueryRow(ctx, `
			INSERT INTO game_daily_rewards(user_id, reward_date, game_id, awarded_xp)
			VALUES ($1, (NOW() AT TIME ZONE 'UTC')::date, $2, $3)
			ON CONFLICT (user_id, reward_date) DO NOTHING
			RETURNING awarded_xp
		`, userID, gameID, numberGameRewardXP).Scan(&awarded)
		switch {
		case err == nil:
			game.XPAwarded = awarded
			_, err = tx.Exec(ctx, `
				INSERT INTO player_progress(user_id, total_xp)
				VALUES($1, $2)
				ON CONFLICT (user_id)
				DO UPDATE SET total_xp=player_progress.total_xp + EXCLUDED.total_xp
			`, userID, awarded)
		case errors.Is(err, pgx.ErrNoRows):
			err = nil // The player has already received today's game reward.
		}
		if err != nil {
			return NumberGameResponse{}, err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return NumberGameResponse{}, err
	}
	return game, nil
}

func (h *Handler) NumberGameStart(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	u, _ := userFromContext(r.Context())
	game, err := h.s.repo.StartNumberGame(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, game)
}

func (h *Handler) NumberGameCurrent(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	u, _ := userFromContext(r.Context())
	game, err := h.s.repo.CurrentNumberGame(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, game)
}

func (h *Handler) NumberGameGuess(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req struct {
		GameID string `json:"game_id"`
		Guess  int    `json:"guess"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil ||
		!gameIDPattern.MatchString(req.GameID) || req.Guess < 1 || req.Guess > 20 {
		http.Error(w, "Invalid game ID or guess (1-20)", http.StatusBadRequest)
		return
	}
	u, _ := userFromContext(r.Context())
	game, err := h.s.repo.GuessNumberGame(r.Context(), u.ID, req.GameID, req.Guess)
	switch {
	case errors.Is(err, errGameNotFound):
		http.Error(w, "Game not found", http.StatusNotFound)
	case errors.Is(err, errGameFinished):
		http.Error(w, "Game already finished", http.StatusConflict)
	case err != nil:
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	default:
		jsonResponse(w, game)
	}
}
