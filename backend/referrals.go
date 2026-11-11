package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type ReferralSummary struct {
	Code         string `json:"code"`
	InvitedCount int64  `json:"invited_count"`
}

func newReferralCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(buf)), nil
}

// GetReferralSummary returns only the current account's code and invite count.
// It never exposes the names, emails or IDs of people who registered.
func (r *Repository) GetReferralSummary(ctx context.Context, userID string) (ReferralSummary, error) {
	var result ReferralSummary
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(referral_code, ''),
		       (SELECT COUNT(*) FROM users invited WHERE invited.referred_by = u.id)
		FROM users u WHERE u.id = $1
	`, userID).Scan(&result.Code, &result.InvitedCount)
	if err != nil {
		return ReferralSummary{}, err
	}
	if result.Code != "" {
		return result, nil
	}
	// Compatibility for test accounts created directly via legacy SQL helpers.
	code, err := newReferralCode()
	if err != nil {
		return ReferralSummary{}, err
	}
	err = r.db.QueryRow(ctx, `
		UPDATE users SET referral_code = COALESCE(referral_code, $1)
		WHERE id = $2
		RETURNING referral_code
	`, code, userID).Scan(&result.Code)
	return result, err
}

func (h *Handler) Referrals(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	user, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	result, err := h.s.repo.GetReferralSummary(r.Context(), user.ID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		http.Error(w, "User not found", http.StatusNotFound)
	case err != nil:
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	default:
		jsonResponse(w, result)
	}
}
