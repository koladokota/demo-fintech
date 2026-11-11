package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
)

const (
	dailyMissionRewardCoins int64 = 250
	purchaseMissionCoins    int64 = 200
	levelTwoMissionCoins    int64 = 350
)

var (
	errMissionUnknown        = errors.New("unknown mission")
	errMissionNotCompleted   = errors.New("mission is not completed")
	errMissionAlreadyClaimed = errors.New("mission reward already claimed")
)

type Mission struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	RewardCoins int64  `json:"reward_coins"`
	Daily       bool   `json:"daily"`
	Completed   bool   `json:"completed"`
	Claimed     bool   `json:"claimed"`
}

type MissionClaimResult struct {
	MissionID    string `json:"mission_id"`
	CoinsAwarded int64  `json:"coins_awarded"`
	Balance      int64  `json:"balance"`
}

type missionFacts struct {
	dailyCheckin, firstPurchase, levelTwo bool
	dailyClaim, purchaseClaim, levelClaim bool
}

type missionQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getMissionFacts(ctx context.Context, q missionQuery, userID string) (missionFacts, error) {
	var facts missionFacts
	err := q.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM daily_checkins d
				WHERE d.user_id = u.id AND d.checkin_date = (NOW() AT TIME ZONE 'UTC')::date
			),
			EXISTS (SELECT 1 FROM user_inventory i WHERE i.user_id = u.id),
			COALESCE((SELECT p.total_xp FROM player_progress p WHERE p.user_id = u.id), 0) >= 100,
			EXISTS (
				SELECT 1 FROM mission_claims m
				WHERE m.user_id = u.id AND m.mission_id = 'daily_checkin'
				  AND m.period_key = TO_CHAR((NOW() AT TIME ZONE 'UTC')::date, 'YYYY-MM-DD')
			),
			EXISTS (
				SELECT 1 FROM mission_claims m
				WHERE m.user_id = u.id AND m.mission_id = 'first_purchase' AND m.period_key = 'once'
			),
			EXISTS (
				SELECT 1 FROM mission_claims m
				WHERE m.user_id = u.id AND m.mission_id = 'reach_level_two' AND m.period_key = 'once'
			)
		FROM users u WHERE u.id = $1
	`, userID).Scan(&facts.dailyCheckin, &facts.firstPurchase, &facts.levelTwo,
		&facts.dailyClaim, &facts.purchaseClaim, &facts.levelClaim)
	return facts, err
}

func missionList(f missionFacts) []Mission {
	return []Mission{
		{
			ID: "daily_checkin", Title: "Ежедневная активность",
			Description: "Получи ежедневные +25 XP в профиле, затем забери монеты здесь",
			RewardCoins: dailyMissionRewardCoins, Daily: true,
			Completed: f.dailyCheckin, Claimed: f.dailyClaim,
		},
		{
			ID: "first_purchase", Title: "Первая покупка",
			Description: "Купи первый предмет в магазине",
			RewardCoins: purchaseMissionCoins,
			Completed: f.firstPurchase, Claimed: f.purchaseClaim,
		},
		{
			ID: "reach_level_two", Title: "Достигни второго уровня",
			Description: "Накопи не менее 100 XP",
			RewardCoins: levelTwoMissionCoins,
			Completed: f.levelTwo, Claimed: f.levelClaim,
		},
	}
}

func (r *Repository) GetMissions(ctx context.Context, userID string) ([]Mission, error) {
	facts, err := getMissionFacts(ctx, r.db, userID)
	if err != nil {
		return nil, err
	}
	return missionList(facts), nil
}

func missionPeriod(id string) (reward int64, period string, err error) {
	switch id {
	case "daily_checkin":
		// The date is resolved by PostgreSQL at transaction time when inserting.
		return dailyMissionRewardCoins, "", nil
	case "first_purchase":
		return purchaseMissionCoins, "once", nil
	case "reach_level_two":
		return levelTwoMissionCoins, "once", nil
	default:
		return 0, "", errMissionUnknown
	}
}

// ClaimMission verifies completion, records one claim, credits the wallet and
// inserts a transaction ledger entry atomically. A user-row lock serializes
// concurrent reward requests and balance changes for the same account.
func (r *Repository) ClaimMission(ctx context.Context, userID, id string) (MissionClaimResult, error) {
	reward, period, err := missionPeriod(id)
	if err != nil {
		return MissionClaimResult{}, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return MissionClaimResult{}, err
	}
	defer tx.Rollback(ctx)

	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedID); err != nil {
		return MissionClaimResult{}, err
	}
	facts, err := getMissionFacts(ctx, tx, userID)
	if err != nil {
		return MissionClaimResult{}, err
	}
	for _, mission := range missionList(facts) {
		if mission.ID != id {
			continue
		}
		if mission.Claimed {
			return MissionClaimResult{}, errMissionAlreadyClaimed
		}
		if !mission.Completed {
			return MissionClaimResult{}, errMissionNotCompleted
		}
	}

	// Keep the date calculation inside the DB transaction, so the daily limit
	// has the same clock source as daily_checkins.
	var inserted int64
	err = tx.QueryRow(ctx, `
		INSERT INTO mission_claims(user_id, mission_id, period_key, reward_coins)
		VALUES ($1, $2, COALESCE(NULLIF($3, ''),
			TO_CHAR((NOW() AT TIME ZONE 'UTC')::date, 'YYYY-MM-DD')), $4)
		ON CONFLICT (user_id, mission_id, period_key) DO NOTHING
		RETURNING reward_coins
	`, userID, id, period, reward).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return MissionClaimResult{}, errMissionAlreadyClaimed
	}
	if err != nil {
		return MissionClaimResult{}, err
	}

	var balance int64
	err = tx.QueryRow(ctx, `
		UPDATE users SET virtual_balance = virtual_balance + $1
		WHERE id = $2 RETURNING virtual_balance
	`, inserted, userID).Scan(&balance)
	if err != nil {
		return MissionClaimResult{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_transactions(user_id, amount, type, description)
		VALUES($1, $2, 'mission-reward', $3)
	`, userID, inserted, "Награда за миссию: "+missionTitle(id))
	if err != nil {
		return MissionClaimResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MissionClaimResult{}, err
	}
	return MissionClaimResult{MissionID: id, CoinsAwarded: inserted, Balance: balance}, nil
}

func missionTitle(id string) string {
	for _, mission := range missionList(missionFacts{}) {
		if mission.ID == id {
			return mission.Title
		}
	}
	return "Задание"
}

func (h *Handler) Missions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	missions, err := h.s.repo.GetMissions(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, missions)
}

func (h *Handler) MissionClaim(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		MissionID string `json:"mission_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "Only one JSON object is allowed", http.StatusBadRequest)
		return
	}
	result, err := h.s.repo.ClaimMission(r.Context(), u.ID, req.MissionID)
	switch {
	case errors.Is(err, errMissionUnknown):
		http.Error(w, "Unknown mission", http.StatusBadRequest)
	case errors.Is(err, errMissionNotCompleted):
		http.Error(w, "Mission not completed yet", http.StatusConflict)
	case errors.Is(err, errMissionAlreadyClaimed):
		http.Error(w, "Mission reward already claimed", http.StatusConflict)
	case err != nil:
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	default:
		jsonResponse(w, result)
	}
}
