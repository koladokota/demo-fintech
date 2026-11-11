package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxDemoWalletAmount int64 = 100000

type DemoWalletTransfer struct {
	Kind   string `json:"kind"`
	Amount int64  `json:"amount"`
}

func validateDemoWalletTransfer(req DemoWalletTransfer) error {
	if req.Amount < 1 || req.Amount > maxDemoWalletAmount {
		return errInvalidInput
	}
	if req.Kind != "deposit" && req.Kind != "withdraw" {
		return errInvalidInput
	}
	return nil
}

// DemoWalletTransfer changes only virtual coins in the database.
// It never interacts with bank cards, payment providers or payout accounts.
// Both directions are disabled unless the app is explicitly in demo mode.
func (h *Handler) DemoWalletTransfer(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if !h.s.demoCredits {
		http.Error(w, "Demo wallet operations are unavailable", http.StatusForbidden)
		return
	}
	var req DemoWalletTransfer
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "Invalid wallet operation", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "Only one JSON object is allowed", http.StatusBadRequest)
		return
	}
	if err := validateDemoWalletTransfer(req); err != nil {
		http.Error(w, "Use deposit or withdraw with 1-100000 virtual coins", http.StatusBadRequest)
		return
	}

	user, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	change := req.Amount
	typ := "demo-deposit"
	description := "Тестовое пополнение виртуального баланса"
	if req.Kind == "withdraw" {
		change = -req.Amount
		typ = "demo-withdraw"
		description = "Тестовое списание (без перевода денег)"
	}
	balance, err := h.s.repo.ChangeBalance(r.Context(), user.ID, change, typ, description)
	switch {
	case errors.Is(err, errInsufficientBalance):
		http.Error(w, "Insufficient virtual balance", http.StatusConflict)
	case err != nil:
		http.Error(w, "Database error", http.StatusInternalServerError)
	default:
		jsonResponse(w, map[string]any{
			"kind": req.Kind,
			"amount": req.Amount,
			"balance": balance,
			"currency": "virtual_coins",
			"real_transfer": false,
		})
	}
}
