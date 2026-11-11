package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestValidateProfile(t *testing.T) {
	for _, tc := range []struct {
		name, avatar string
		valid        bool
	}{
		{"Ada", "rocket", true},
		{"  Николай  ", "planet", true},
		{"A", "initial", false},
		{"", "star", false},
		{strings.Repeat("Я", 33), "bolt", false},
		{"Name\nwith control", "shield", false},
		{"Player", "external-url", false},
	} {
		got, err := validateProfile(UpdateProfileRequest{Name: tc.name, AvatarKey: tc.avatar})
		if tc.valid && err != nil {
			t.Errorf("%q / %q unexpectedly rejected: %v", tc.name, tc.avatar, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("%q / %q unexpectedly accepted: %+v", tc.name, tc.avatar, got)
		}
		if tc.valid && strings.TrimSpace(tc.name) != got.Name {
			t.Errorf("name not trimmed: %+v", got)
		}
	}
}

func TestProfileAchievements(t *testing.T) {
	initial := profileAchievements(ProfileStats{Level: 1})
	if len(initial) != 6 {
		t.Fatalf("initial achievements=%d, want 6", len(initial))
	}
	for _, item := range initial {
		if item.Unlocked {
			t.Fatalf("%s unlocked without progress", item.ID)
		}
	}
	stats := ProfileStats{
		Level: 2, ItemsOwned: 3, GamesWon: 5, DailyCheckins: 3,
	}
	for _, item := range profileAchievements(stats) {
		if !item.Unlocked {
			t.Fatalf("%s should be unlocked: %+v", item.ID, stats)
		}
	}
}

func TestProfileAPI(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL integration test")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	userID, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	itemID := "profile-test-" + userID
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID); err != nil {
			t.Errorf("clean up profile test user: %v", err)
		}
		if _, err := db.Exec(context.Background(), "DELETE FROM shop_items WHERE id=$1", itemID); err != nil {
			t.Errorf("clean up profile test item: %v", err)
		}
	})

	_, err = db.Exec(ctx, `
		INSERT INTO users(id, name, email, password_hash, virtual_balance)
		VALUES ($1, 'Tester', $2, 'hash', 1234)
	`, userID, userID+"@example.test")
	if err != nil {
		t.Fatal(err)
	}

	repo := &Repository{db: db}
	if err := repo.CreateSession(ctx, userID, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO player_progress(user_id, total_xp)
		VALUES ($1, 125)
	`, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO shop_items(id, name, description, price, rarity, icon)
		VALUES($1, 'Test item', 'Test item description', 10, 'common', 'package')
	`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, "INSERT INTO user_inventory(user_id, item_id) VALUES($1, $2)", userID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO game_sessions(user_id, secret_number, attempts_left, status, finished_at)
		VALUES($1, 7, 3, 'won', NOW())
	`, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO daily_checkins(user_id, checkin_date)
		VALUES($1, (NOW() AT TIME ZONE 'UTC')::date)
	`, userID)
	if err != nil {
		t.Fatal(err)
	}

	svc := &Service{repo: repo}
	handler := &Handler{s: svc}
	profile := svc.RequireAuth(http.HandlerFunc(handler.Profile))
	me := svc.RequireAuth(http.HandlerFunc(handler.Me))

	run := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		profile.ServeHTTP(w, req)
		return w
	}

	if w := run("GET", "/api/profile", "", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous profile GET=%d, want 401", w.Code)
	}

	w := run("GET", "/api/profile", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("profile GET: %d %s", w.Code, w.Body.String())
	}
	var profileResponse ProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &profileResponse); err != nil {
		t.Fatal(err)
	}
	if profileResponse.User.Name != "Tester" || profileResponse.User.AvatarKey != "initial" {
		t.Fatalf("incorrect initial user: %+v", profileResponse.User)
	}
	if profileResponse.Stats.Level != 2 || profileResponse.Stats.TotalXP != 125 ||
		profileResponse.Stats.ItemsOwned != 1 || profileResponse.Stats.GamesWon != 1 ||
		profileResponse.Stats.GamesFinished != 1 || profileResponse.Stats.DailyCheckins != 1 {
		t.Fatalf("incorrect real stats: %+v", profileResponse.Stats)
	}
	if len(profileResponse.Achievements) != 6 ||
		!profileResponse.Achievements[0].Unlocked || !profileResponse.Achievements[2].Unlocked ||
		!profileResponse.Achievements[4].Unlocked {
		t.Fatalf("incorrect achievements: %+v", profileResponse.Achievements)
	}

	for _, bad := range []string{
		`{"name":"A","avatar_key":"rocket"}`,
		`{"name":"Tester","avatar_key":"unknown"}`,
		`{"name":"Tester","avatar_key":"rocket","virtual_balance":999999}`,
		`{"name":"Tester","avatar_key":"rocket"} {}`,
	} {
		w := run("PATCH", "/api/profile", bad, true)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid profile %s: status=%d body=%s", bad, w.Code, w.Body.String())
		}
	}
	if w := run("POST", "/api/profile", "", true); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST profile=%d, want 405", w.Code)
	}

	w = run("PATCH", "/api/profile", `{"name":"  Новый игрок  ","avatar_key":"rocket"}`, true)
	if w.Code != http.StatusOK {
		t.Fatalf("profile PATCH: %d %s", w.Code, w.Body.String())
	}
	var updated ProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.User.Name != "Новый игрок" || updated.User.AvatarKey != "rocket" {
		t.Fatalf("incorrect PATCH result: %+v", updated.User)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+token)
	meRec := httptest.NewRecorder()
	me.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /api/me: %d", meRec.Code)
	}
	var meUser User
	if err := json.Unmarshal(meRec.Body.Bytes(), &meUser); err != nil {
		t.Fatal(err)
	}
	if meUser.Name != updated.User.Name || meUser.AvatarKey != "rocket" {
		t.Fatalf("changes not visible in next authorized request: %+v", meUser)
	}

	var balance int64
	var email string
	if err := db.QueryRow(ctx, "SELECT virtual_balance, email FROM users WHERE id=$1", userID).Scan(&balance, &email); err != nil {
		t.Fatal(err)
	}
	if balance != 1234 || email != userID+"@example.test" {
		t.Fatalf("profile update unexpectedly affected sensitive data: balance=%d email=%q", balance, email)
	}
}
