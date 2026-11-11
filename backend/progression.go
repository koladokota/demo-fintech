package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

const dailyXP int64 = 25

var errDailyXPAlreadyClaimed = errors.New("daily XP already claimed")

type PlayerProgress struct {
	Level          int   `json:"level"`
	TotalXP        int64 `json:"total_xp"`
	XPInLevel      int64 `json:"xp_in_level"`
	XPForNextLevel int64 `json:"xp_for_next_level"`
	ClaimedToday   bool  `json:"claimed_today"`
}

func progressFromXP(total int64, claimedToday bool) PlayerProgress {
	if total < 0 {
		total = 0
	}
	level := 1
	remaining := total
	for {
		required := int64(level) * 100
		if remaining < required {
			return PlayerProgress{
				Level:          level,
				TotalXP:        total,
				XPInLevel:      remaining,
				XPForNextLevel: required,
				ClaimedToday:   claimedToday,
			}
		}
		remaining -= required
		level++
	}
}

func (r *Repository) GetProgress(ctx context.Context, userID string) (PlayerProgress, error) {
	var totalXP int64
	var claimedToday bool
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(p.total_xp, 0),
		       EXISTS (
		           SELECT 1 FROM daily_checkins d
		           WHERE d.user_id = u.id
		             AND d.checkin_date = (now() AT TIME ZONE 'UTC')::date
		       )
		FROM users u
		LEFT JOIN player_progress p ON p.user_id = u.id
		WHERE u.id = $1
	`, userID).Scan(&totalXP, &claimedToday)
	if err != nil {
		return PlayerProgress{}, err
	}
	return progressFromXP(totalXP, claimedToday), nil
}

func (r *Repository) ClaimDailyXP(ctx context.Context, userID string) (PlayerProgress, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return PlayerProgress{}, err
	}
	defer tx.Rollback(ctx)

	var claimedUserID string
	err = tx.QueryRow(ctx, `
		INSERT INTO daily_checkins (user_id, checkin_date)
		VALUES ($1, (now() AT TIME ZONE 'UTC')::date)
		ON CONFLICT (user_id, checkin_date) DO NOTHING
		RETURNING user_id
	`, userID).Scan(&claimedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlayerProgress{}, errDailyXPAlreadyClaimed
	}
	if err != nil {
		return PlayerProgress{}, err
	}

	var totalXP int64
	err = tx.QueryRow(ctx, `
		INSERT INTO player_progress (user_id, total_xp)
		VALUES ($1, $2)
		ON CONFLICT (user_id)
		DO UPDATE SET total_xp = player_progress.total_xp + EXCLUDED.total_xp
		RETURNING total_xp
	`, userID, dailyXP).Scan(&totalXP)
	if err != nil {
		return PlayerProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlayerProgress{}, err
	}
	return progressFromXP(totalXP, true), nil
}

func (h *Handler) Progression(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	progress, err := h.s.repo.GetProgress(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, progress)
}

func (h *Handler) DailyCheckIn(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	progress, err := h.s.repo.ClaimDailyXP(r.Context(), u.ID)
	if errors.Is(err, errDailyXPAlreadyClaimed) {
		http.Error(w, "Daily XP already claimed", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, progress)
}
