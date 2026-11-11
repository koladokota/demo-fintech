package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var errProfileInvalid = errors.New("invalid profile fields")

type ProfileStats struct {
	TotalXP       int64 `json:"total_xp"`
	Level         int   `json:"level"`
	ItemsOwned    int64 `json:"items_owned"`
	GamesFinished int64 `json:"games_finished"`
	GamesWon      int64 `json:"games_won"`
	DailyCheckins int64 `json:"daily_checkins"`
}

type ProfileAchievement struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Unlocked    bool   `json:"unlocked"`
}

type ProfileResponse struct {
	User         User                 `json:"user"`
	Stats        ProfileStats         `json:"stats"`
	Achievements []ProfileAchievement `json:"achievements"`
}

type UpdateProfileRequest struct {
	Name      string `json:"name"`
	AvatarKey string `json:"avatar_key"`
}

func validateProfile(req UpdateProfileRequest) (UpdateProfileRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	count := utf8.RuneCountInString(req.Name)
	if count < 2 || count > 32 {
		return UpdateProfileRequest{}, errProfileInvalid
	}
	for _, char := range req.Name {
		if unicode.IsControl(char) {
			return UpdateProfileRequest{}, errProfileInvalid
		}
	}
	switch req.AvatarKey {
	case "initial", "rocket", "planet", "bolt", "star", "shield":
		return req, nil
	default:
		return UpdateProfileRequest{}, errProfileInvalid
	}
}

func profileAchievements(s ProfileStats) []ProfileAchievement {
	return []ProfileAchievement{
		{ID: "first_purchase", Title: "Первая покупка",
			Description: "Приобрести первый предмет", Unlocked: s.ItemsOwned >= 1},
		{ID: "collector", Title: "Коллекционер",
			Description: "Собрать 3 предмета", Unlocked: s.ItemsOwned >= 3},
		{ID: "first_win", Title: "Первая победа",
			Description: "Одержать победу в мини-игре", Unlocked: s.GamesWon >= 1},
		{ID: "five_wins", Title: "Пять побед",
			Description: "Победить в мини-играх 5 раз", Unlocked: s.GamesWon >= 5},
		{ID: "level_two", Title: "Новый уровень",
			Description: "Достичь второго уровня", Unlocked: s.Level >= 2},
		{ID: "three_checkins", Title: "Постоянный игрок",
			Description: "Получить ежедневную награду 3 раза", Unlocked: s.DailyCheckins >= 3},
	}
}

// GetProfile collects only the authenticated account's information.
// Achievements are derived from persisted facts, never from client-supplied counters.
func (r *Repository) GetProfile(ctx context.Context, userID string) (ProfileResponse, error) {
	var profile ProfileResponse
	err := r.db.QueryRow(ctx, `
		SELECT
			u.id, u.name, u.email, u.created_at, u.avatar_key,
			COALESCE(pp.total_xp, 0),
			(SELECT COUNT(*) FROM user_inventory ui WHERE ui.user_id = u.id),
			(SELECT COUNT(*) FROM game_sessions gs WHERE gs.user_id = u.id AND gs.status IN ('won', 'lost')),
			(SELECT COUNT(*) FROM game_sessions gs WHERE gs.user_id = u.id AND gs.status = 'won'),
			(SELECT COUNT(*) FROM daily_checkins d WHERE d.user_id = u.id)
		FROM users u
		LEFT JOIN player_progress pp ON pp.user_id = u.id
		WHERE u.id = $1
	`, userID).Scan(
		&profile.User.ID, &profile.User.Name, &profile.User.Email,
		&profile.User.CreatedAt, &profile.User.AvatarKey,
		&profile.Stats.TotalXP,
		&profile.Stats.ItemsOwned,
		&profile.Stats.GamesFinished,
		&profile.Stats.GamesWon,
		&profile.Stats.DailyCheckins,
	)
	if err != nil {
		return ProfileResponse{}, err
	}
	profile.Stats.Level = progressFromXP(profile.Stats.TotalXP, false).Level
	profile.Achievements = profileAchievements(profile.Stats)
	return profile, nil
}

func (r *Repository) UpdateProfile(ctx context.Context, userID string, req UpdateProfileRequest) (ProfileResponse, error) {
	req, err := validateProfile(req)
	if err != nil {
		return ProfileResponse{}, err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE users SET name = $1, avatar_key = $2 WHERE id = $3
	`, req.Name, req.AvatarKey, userID)
	if err != nil {
		return ProfileResponse{}, err
	}
	if tag.RowsAffected() != 1 {
		return ProfileResponse{}, pgx.ErrNoRows
	}
	return r.GetProfile(ctx, userID)
}

func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	u, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		profile, err := h.s.repo.GetProfile(r.Context(), u.ID)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		jsonResponse(w, profile)

	case http.MethodPatch:
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		var req UpdateProfileRequest
		if err := decoder.Decode(&req); err != nil {
			http.Error(w, "Invalid profile data", http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "Only one JSON object is allowed", http.StatusBadRequest)
			return
		}

		profile, err := h.s.repo.UpdateProfile(r.Context(), u.ID, req)
		switch {
		case errors.Is(err, errProfileInvalid):
			http.Error(w, "Name must have 2-32 characters and avatar must be allowed", http.StatusBadRequest)
		case errors.Is(err, pgx.ErrNoRows):
			http.Error(w, "Profile not found", http.StatusNotFound)
		case err != nil:
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		default:
			jsonResponse(w, profile)
		}

	default:
		w.Header().Set("Allow", "GET, PATCH")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
