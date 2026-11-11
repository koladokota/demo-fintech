package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCompareGuess(t *testing.T) {
	for _, tc := range []struct {
		secret, guess int
		want          string
	}{
		{13, 5, "higher"},
		{13, 17, "lower"},
		{13, 13, "correct"},
	} {
		if got := compareGuess(tc.secret, tc.guess); got != tc.want {
			t.Fatalf("compareGuess(%d, %d)=%q, want %q", tc.secret, tc.guess, got, tc.want)
		}
	}
}

func numberGameTestRepository(t *testing.T) (*Repository, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for the number game integration tests")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	id, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO users(id,name,email,password_hash)
		VALUES($1,'Number game test',$2,'test')
	`, id, id+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	// FK cascade removes game_sessions, daily rewards and player_progress.
	// Cleanup runs before db.Close because t.Cleanup executes LIFO.
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DELETE FROM users WHERE id=$1", id); err != nil {
			t.Errorf("clean up number game test data: %v", err)
		}
	})
	return &Repository{db: db}, id
}

func storedGameSecret(t *testing.T, r *Repository, gameID string) int {
	t.Helper()
	var secret int
	err := r.db.QueryRow(context.Background(), `
		SELECT secret_number FROM game_sessions WHERE id=$1
	`, gameID).Scan(&secret)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func differentGuess(secret int) int {
	if secret == 20 {
		return 19
	}
	return secret + 1
}

func TestNumberGameWinAndDailyReward(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()

	game, err := repo.StartNumberGame(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if game.Status != "active" || game.AttemptsLeft != 5 || game.GameID == "" || game.SecretNumber != nil {
		t.Fatalf("bad initial game: %+v", game)
	}
	payload, err := json.Marshal(game)
	if err != nil {
		t.Fatal(err)
	}
	var exposed map[string]any
	if err = json.Unmarshal(payload, &exposed); err != nil {
		t.Fatal(err)
	}
	if _, ok := exposed["secret_number"]; ok {
		t.Fatal("secret_number leaked in an active game response")
	}

	restored, err := repo.CurrentNumberGame(ctx, userID)
	if err != nil || restored.GameID != game.GameID || restored.SecretNumber != nil {
		t.Fatalf("cannot restore active game safely: %+v, %v", restored, err)
	}
	secret := storedGameSecret(t, repo, game.GameID)
	wrong, err := repo.GuessNumberGame(ctx, userID, game.GameID, differentGuess(secret))
	if err != nil || wrong.Status != "active" || wrong.AttemptsLeft != 4 || wrong.SecretNumber != nil || wrong.XPAwarded != 0 {
		t.Fatalf("bad wrong guess: %+v, %v", wrong, err)
	}
	won, err := repo.GuessNumberGame(ctx, userID, game.GameID, secret)
	if err != nil || won.Status != "won" || won.Result != "correct" || won.XPAwarded != numberGameRewardXP || won.SecretNumber == nil || *won.SecretNumber != secret {
		t.Fatalf("bad win: %+v, %v", won, err)
	}
	_, err = repo.GuessNumberGame(ctx, userID, game.GameID, secret)
	if !errors.Is(err, errGameFinished) {
		t.Fatalf("repeated guess should be rejected: %v", err)
	}

	newGame, err := repo.StartNumberGame(ctx, userID)
	if err != nil || newGame.GameID == game.GameID {
		t.Fatalf("new game was not created: %+v, %v", newGame, err)
	}
	secret2 := storedGameSecret(t, repo, newGame.GameID)
	wonAgain, err := repo.GuessNumberGame(ctx, userID, newGame.GameID, secret2)
	if err != nil || wonAgain.Status != "won" || wonAgain.XPAwarded != 0 {
		t.Fatalf("a second win must not award XP today: %+v, %v", wonAgain, err)
	}

	var xp int64
	if err := repo.db.QueryRow(ctx, "SELECT total_xp FROM player_progress WHERE user_id=$1", userID).Scan(&xp); err != nil {
		t.Fatal(err)
	}
	if xp != numberGameRewardXP {
		t.Fatalf("total XP=%d, want %d", xp, numberGameRewardXP)
	}
	var rewards int
	if err := repo.db.QueryRow(ctx, "SELECT count(*) FROM game_daily_rewards WHERE user_id=$1", userID).Scan(&rewards); err != nil {
		t.Fatal(err)
	}
	if rewards != 1 {
		t.Fatalf("daily rewards=%d, want 1", rewards)
	}
}

func TestNumberGameFiveWrongGuesses(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()
	game, err := repo.StartNumberGame(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	secret := storedGameSecret(t, repo, game.GameID)
	for attempt := 1; attempt <= numberGameAttempts; attempt++ {
		result, err := repo.GuessNumberGame(ctx, userID, game.GameID, differentGuess(secret))
		if err != nil {
			t.Fatal(err)
		}
		expectedStatus := "active"
		if attempt == numberGameAttempts {
			expectedStatus = "lost"
		}
		if result.Status != expectedStatus || result.AttemptsLeft != numberGameAttempts-attempt || result.XPAwarded != 0 {
			t.Fatalf("attempt %d: unexpected state %+v", attempt, result)
		}
	}
	state, err := repo.CurrentNumberGame(ctx, userID)
	if err != nil || state.Status != "lost" || state.SecretNumber == nil || *state.SecretNumber != secret {
		t.Fatalf("failed to restore finished game: %+v, %v", state, err)
	}
	var rewards int
	if err := repo.db.QueryRow(ctx, "SELECT count(*) FROM game_daily_rewards WHERE user_id=$1", userID).Scan(&rewards); err != nil {
		t.Fatal(err)
	}
	if rewards != 0 {
		t.Fatalf("a lost game granted %d rewards", rewards)
	}
}

func TestNumberGameConcurrentStartsAndGuesses(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan NumberGameResponse, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			game, err := repo.StartNumberGame(ctx, userID)
			results <- game
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	var gameID string
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent start failed: %v", err)
		}
	}
	for game := range results {
		if gameID == "" {
			gameID = game.GameID
		}
		if gameID != game.GameID {
			t.Fatalf("concurrent starts created different active games: %q, %q", gameID, game.GameID)
		}
	}
	var activeCount int
	if err := repo.db.QueryRow(ctx, "SELECT count(*) FROM game_sessions WHERE user_id=$1 AND status='active'", userID).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("active game rows=%d, want 1", activeCount)
	}

	secret := storedGameSecret(t, repo, gameID)
	guessStart := make(chan struct{})
	guessErrs := make(chan error, 2)
	guessResults := make(chan NumberGameResponse, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-guessStart
			game, err := repo.GuessNumberGame(ctx, userID, gameID, secret)
			guessErrs <- err
			guessResults <- game
		}()
	}
	close(guessStart)
	wg.Wait()
	close(guessErrs)
	close(guessResults)
	wins, finished := 0, 0
	for err := range guessErrs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, errGameFinished):
			finished++
		default:
			t.Fatalf("unexpected concurrent guess error: %v", err)
		}
	}
	if wins != 1 || finished != 1 {
		t.Fatalf("concurrent guesses: successful=%d finished=%d, want one each", wins, finished)
	}
	awarded := int64(0)
	for game := range guessResults {
		awarded += game.XPAwarded
	}
	if awarded != numberGameRewardXP {
		t.Fatalf("awarded XP=%d, want %d", awarded, numberGameRewardXP)
	}
}
