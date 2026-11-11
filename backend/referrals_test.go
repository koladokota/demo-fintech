package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestReferralCodeGenerator(t *testing.T) {
	a, err := newReferralCode()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newReferralCode()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-F0-9]{16}$`).MatchString(a) {
		t.Fatalf("invalid referral code %q", a)
	}
	if a == b {
		t.Fatalf("random referral codes unexpectedly identical: %q", a)
	}
}

func TestReferralRegistrationAndPrivacy(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()
	svc := &Service{repo: repo}

	// Simulate an existing account without a referral code.
	parent, err := repo.GetReferralSummary(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Code == "" || parent.InvitedCount != 0 {
		t.Fatalf("unexpected initial referral state: %+v", parent)
	}
	parentAgain, err := repo.GetReferralSummary(ctx, userID)
	if err != nil || parent.Code != parentAgain.Code {
		t.Fatalf("referral code not stable: %+v %+v %v", parent, parentAgain, err)
	}

	newEmail, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	registration, err := svc.Register(ctx, AuthRequest{
		Name: "Invited player", Email: newEmail+"@example.test",
		Password: "securePassword123", ReferralCode: strings.ToLower(parent.Code),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := repo.db.Exec(context.Background(), "DELETE FROM users WHERE id=$1", registration.User.ID); err != nil {
			t.Errorf("cleanup invited user: %v", err)
		}
	})
	if registration.User.ID == userID || registration.Token == "" {
		t.Fatalf("registration returned invalid user/session: %+v", registration)
	}

	var referredBy string
	err = repo.db.QueryRow(ctx, "SELECT referred_by FROM users WHERE id=$1", registration.User.ID).Scan(&referredBy)
	if err != nil || referredBy != userID {
		t.Fatalf("wrong referral attribution: referredBy=%q err=%v", referredBy, err)
	}
	parent, err = repo.GetReferralSummary(ctx, userID)
	if err != nil || parent.InvitedCount != 1 {
		t.Fatalf("referral count mismatch: %+v %v", parent, err)
	}

	child, err := repo.GetReferralSummary(ctx, registration.User.ID)
	if err != nil || child.Code == "" || child.Code == parent.Code || child.InvitedCount != 0 {
		t.Fatalf("invited user should receive independent invite code: %+v %v", child, err)
	}

	var balance int64
	if err := repo.db.QueryRow(ctx, "SELECT virtual_balance FROM users WHERE id=$1", userID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("referral unexpectedly credited coins: %d", balance)
	}
	if _, err := svc.Register(ctx, AuthRequest{
		Name: "Invalid referral", Email: "invalid-"+newEmail+"@example.test",
		Password: "securePassword123", ReferralCode: "INVALIDCODE",
	}); !errors.Is(err, errInvalidReferral) {
		t.Fatalf("expected errInvalidReferral, got %v", err)
	}

	// The route returns only a code and a count (no invited person's details).
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSession(ctx, userID, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	handler := svc.RequireAuth(http.HandlerFunc((&Handler{s: svc}).Referrals))
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/referrals", nil))
	if anonymous.Code != 401 {
		t.Fatalf("anonymous status=%d", anonymous.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/referrals", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("referral endpoint status=%d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, parent.Code) || !strings.Contains(body, `"invited_count":1`) ||
		strings.Contains(body, newEmail) || strings.Contains(body, registration.User.Name) {
		t.Fatalf("referral response leaks user details or omits code/count: %s", body)
	}

	if _, err := repo.LookupReferrer(ctx, parent.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LookupReferrer(ctx, "UNKNOWN"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown code lookup expected no rows, got %v", err)
	}
}
