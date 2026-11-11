package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func missionByID(t *testing.T, missions []Mission, id string) Mission {
	t.Helper()
	for _, m := range missions {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("mission %s not found", id)
	return Mission{}
}

func TestMissionCatalog(t *testing.T) {
	items := missionList(missionFacts{dailyCheckin: true, dailyClaim: true, levelTwo: true})
	if len(items) != 3 {
		t.Fatalf("missions=%d, want 3", len(items))
	}
	daily := missionByID(t, items, "daily_checkin")
	if !daily.Daily || !daily.Claimed || !daily.Completed || daily.RewardCoins != 250 {
		t.Fatalf("daily mission mismatch: %+v", daily)
	}
	purchase := missionByID(t, items, "first_purchase")
	if purchase.Completed || purchase.Claimed || purchase.Daily {
		t.Fatalf("first purchase should not be complete: %+v", purchase)
	}
	for _, id := range []string{"daily_checkin", "first_purchase", "reach_level_two"} {
		if _, _, err := missionPeriod(id); err != nil {
			t.Errorf("known mission rejected: %s", id)
		}
	}
	if _, _, err := missionPeriod("other"); !errors.Is(err, errMissionUnknown) {
		t.Fatalf("unknown mission error=%v", err)
	}
}

func TestMissionRewardsInPostgres(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()

	initial, err := repo.GetMissions(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 3 {
		t.Fatalf("initial mission count=%d", len(initial))
	}
	for _, item := range initial {
		if item.Completed || item.Claimed {
			t.Fatalf("new user mission is already completed: %+v", item)
		}
		_, err := repo.ClaimMission(ctx, userID, item.ID)
		if !errors.Is(err, errMissionNotCompleted) {
			t.Fatalf("uncompleted mission %s claim error=%v", item.ID, err)
		}
	}

	_, err = repo.db.Exec(ctx, `
		INSERT INTO daily_checkins(user_id, checkin_date)
		VALUES($1, (NOW() AT TIME ZONE 'UTC')::date)
	`, userID)
	if err != nil {
		t.Fatal(err)
	}

	// Concurrent claims on the same account must award coins only once.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	rewards := make(chan MissionClaimResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := repo.ClaimMission(ctx, userID, "daily_checkin")
			errs <- err
			rewards <- result
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(rewards)
	success, duplicates := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, errMissionAlreadyClaimed):
			duplicates++
		default:
			t.Fatalf("concurrent claim error=%v", err)
		}
	}
	if success != 1 || duplicates != 1 {
		t.Fatalf("concurrent result: successes=%d duplicates=%d", success, duplicates)
	}
	awarded := int64(0)
	for result := range rewards {
		awarded += result.CoinsAwarded
	}
	if awarded != dailyMissionRewardCoins {
		t.Fatalf("awarded=%d, want %d", awarded, dailyMissionRewardCoins)
	}
	if _, err := repo.ClaimMission(ctx, userID, "daily_checkin"); !errors.Is(err, errMissionAlreadyClaimed) {
		t.Fatalf("repeat daily claim error=%v", err)
	}

	itemID := "mission-test-" + userID
	_, err = repo.db.Exec(ctx, `
		INSERT INTO shop_items(id, name, description, price, rarity)
		VALUES($1, 'Mission test', 'Test item', 100, 'common')
	`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := repo.db.Exec(context.Background(),
			"DELETE FROM user_inventory WHERE user_id=$1 AND item_id=$2", userID, itemID); err != nil {
			t.Errorf("cleanup inventory: %v", err)
		}
		if _, err := repo.db.Exec(context.Background(), "DELETE FROM shop_items WHERE id=$1", itemID); err != nil {
			t.Errorf("cleanup shop item: %v", err)
		}
	})
	_, err = repo.db.Exec(ctx, "INSERT INTO user_inventory(user_id, item_id) VALUES($1,$2)", userID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.Exec(ctx, "INSERT INTO player_progress(user_id,total_xp) VALUES($1,125)", userID)
	if err != nil {
		t.Fatal(err)
	}
	missions, err := repo.GetMissions(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !missionByID(t, missions, "first_purchase").Completed ||
		!missionByID(t, missions, "reach_level_two").Completed ||
		!missionByID(t, missions, "daily_checkin").Claimed {
		t.Fatalf("mission states not refreshed: %+v", missions)
	}

	claimPurchase, err := repo.ClaimMission(ctx, userID, "first_purchase")
	if err != nil || claimPurchase.CoinsAwarded != purchaseMissionCoins {
		t.Fatalf("purchase claim=%+v err=%v", claimPurchase, err)
	}
	claimLevel, err := repo.ClaimMission(ctx, userID, "reach_level_two")
	if err != nil || claimLevel.CoinsAwarded != levelTwoMissionCoins {
		t.Fatalf("level 2 claim=%+v err=%v", claimLevel, err)
	}
	if _, err := repo.ClaimMission(ctx, userID, "first_purchase"); !errors.Is(err, errMissionAlreadyClaimed) {
		t.Fatalf("duplicate one-time reward should fail: %v", err)
	}

	want := dailyMissionRewardCoins + purchaseMissionCoins + levelTwoMissionCoins
	var balance, ledgerSum int64
	if err := repo.db.QueryRow(ctx, "SELECT virtual_balance FROM users WHERE id=$1", userID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount),0) FROM wallet_transactions
		WHERE user_id=$1 AND type='mission-reward'
	`, userID).Scan(&ledgerSum); err != nil {
		t.Fatal(err)
	}
	var claims int
	if err := repo.db.QueryRow(ctx, "SELECT COUNT(*) FROM mission_claims WHERE user_id=$1", userID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if balance != want || ledgerSum != want || claims != 3 {
		t.Fatalf("unexpected reward state: balance=%d ledger=%d claims=%d, want %d/3", balance, ledgerSum, claims, want)
	}
}

func TestMissionClaimRequiresAuthAndValidID(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSession(ctx, userID, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repo}
	h := &Handler{s: svc}
	claim := svc.RequireAuth(http.HandlerFunc(h.MissionClaim))
	missions := svc.RequireAuth(http.HandlerFunc(h.Missions))

	send := func(handler http.Handler, path, method, payload string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if w := send(missions, "/api/missions", "GET", "", false); w.Code != 401 {
		t.Fatalf("unauthenticated mission status=%d", w.Code)
	}
	if w := send(claim, "/api/missions/claim", "POST",
		`{"mission_id":"daily_checkin"}`, false); w.Code != 401 {
		t.Fatalf("unauthenticated claim status=%d", w.Code)
	}
	for _, payload := range []string{
		`{"mission_id":"other"}`, `{"mission_id":"daily_checkin","coins":1000000}`,
		`{"mission_id":"daily_checkin"}{}`, `{"mission_id":`,
	} {
		w := send(claim, "/api/missions/claim", "POST", payload, true)
		if w.Code != 400 {
			t.Fatalf("invalid payload %q status=%d body=%s", payload, w.Code, w.Body.String())
		}
	}
	w := send(missions, "/api/missions", "GET", "", true)
	if w.Code != 200 {
		t.Fatalf("GET mission status=%d body=%s", w.Code, w.Body.String())
	}
	var data []Mission
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || len(data) != 3 {
		t.Fatalf("bad mission response: %+v err=%v", data, err)
	}
	w = send(claim, "/api/missions/claim", "POST", `{"mission_id":"daily_checkin"}`, true)
	if w.Code != 409 {
		t.Fatalf("uncompleted daily reward status=%d body=%s", w.Code, w.Body.String())
	}
}
