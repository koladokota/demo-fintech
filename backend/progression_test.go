package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProgressFromXP(t *testing.T) {
	cases := []struct {
		xp          int64
		level       int
		inLevel     int64
		forNext     int64
	}{
		{0, 1, 0, 100},
		{25, 1, 25, 100},
		{99, 1, 99, 100},
		{100, 2, 0, 200},
		{299, 2, 199, 200},
		{300, 3, 0, 300},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.xp), func(t *testing.T) {
			got := progressFromXP(tc.xp, false)
			if got.Level != tc.level || got.XPInLevel != tc.inLevel ||
				got.XPForNextLevel != tc.forNext || got.TotalXP != tc.xp {
				t.Fatalf("progressFromXP(%d) = %+v", tc.xp, got)
			}
		})
	}
}

func TestDailyXPConcurrentClaims(t *testing.T) {
	dsn := getenvTestDatabaseURL()
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for the PostgreSQL integration test")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	// The TEST_DATABASE_URL must point to an isolated test database.
	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("apply test migrations: %v", err)
	}

	userID := fmt.Sprintf("daily-xp-test-%d", time.Now().UnixNano())
	_, err = db.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash)
		VALUES ($1, 'Daily XP test', $2, 'test')
	`, userID, userID+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID); err != nil {
			t.Errorf("cleanup daily XP test user: %v", err)
		}
	})
	repo := &Repository{db: db}

	before, err := repo.GetProgress(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if before.TotalXP != 0 || before.Level != 1 || before.ClaimedToday {
		t.Fatalf("unexpected initial state: %+v", before)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := repo.ClaimDailyXP(ctx, userID)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes, alreadyClaimed := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errDailyXPAlreadyClaimed):
			alreadyClaimed++
		default:
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if successes != 1 || alreadyClaimed != 1 {
		t.Fatalf("successes=%d conflicts=%d, want one each", successes, alreadyClaimed)
	}
	after, err := repo.GetProgress(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TotalXP != dailyXP || after.Level != 1 || !after.ClaimedToday {
		t.Fatalf("unexpected progression: %+v", after)
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM daily_checkins WHERE user_id=$1", userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("checkin rows = %d, want 1", count)
	}
}

func getenvTestDatabaseURL() string {
	return os.Getenv("TEST_DATABASE_URL")
}
