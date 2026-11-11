package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLogoutRevokesSession(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := runMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	userID, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash)
		VALUES ($1, 'Logout Test', $2, 'test')
	`, userID, userID+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID); err != nil {
			t.Errorf("clean up logout test user: %v", err)
		}
	})
	repo := &Repository{db: db}
	if err := repo.CreateSession(ctx, userID, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repo}
	h := &Handler{s: svc}

	logout := svc.RequireAuth(http.HandlerFunc(h.Logout))
	me := svc.RequireAuth(http.HandlerFunc(h.Me))

	newRequest := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}

	before := httptest.NewRecorder()
	me.ServeHTTP(before, newRequest(http.MethodGet, "/api/me"))
	if before.Code != http.StatusOK {
		t.Fatalf("before logout: status=%d body=%s", before.Code, before.Body.String())
	}

	ended := httptest.NewRecorder()
	logout.ServeHTTP(ended, newRequest(http.MethodPost, "/api/logout"))
	if ended.Code != http.StatusNoContent {
		t.Fatalf("logout: status=%d body=%s", ended.Code, ended.Body.String())
	}

	after := httptest.NewRecorder()
	me.ServeHTTP(after, newRequest(http.MethodGet, "/api/me"))
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: status=%d body=%s, want 401", after.Code, after.Body.String())
	}

	var count int
	err = db.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", userID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("remaining sessions: %d, want 0", count)
	}
}
