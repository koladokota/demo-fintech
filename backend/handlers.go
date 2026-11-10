package main

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
)

// App is retained as a small compatibility adapter for existing package tests.
type App struct{ db *pgxpool.Pool }

func (a *App) shopInventory(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	s := &Service{repo: &Repository{db: a.db}}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	auth := s.RequireAuth(http.HandlerFunc((&Handler{s: s}).ShopInventory))
	auth.ServeHTTP(w, r)
}

type Handler struct{ s *Service }

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req AuthRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	res, err := h.s.Register(r.Context(), req)
	if err != nil {
		if errors.Is(err, errInvalidInput) {
			http.Error(w, "Invalid name, email or password (minimum 8 characters)", 400)
		} else if errors.Is(err, errConflict) {
			http.Error(w, "Email may already be registered", 409)
		} else {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}
	jsonResponse(w, res)
}
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req AuthRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	res, err := h.s.Login(r.Context(), req)
	if err != nil {
		if !errors.Is(err, errUnauthorized) {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		http.Error(w, "Invalid email or password", 401)
		return
	}
	jsonResponse(w, res)
}
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", 401)
		return
	}
	jsonResponse(w, u)
}
func (h *Handler) Wallet(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	u, _ := userFromContext(r.Context())
	balance, txs, err := h.s.repo.Wallet(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	jsonResponse(w, map[string]any{"balance": balance, "transactions": txs})
}
func (h *Handler) DemoAdd(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	u, _ := userFromContext(r.Context())
	balance, err := h.s.AddDemo(r.Context(), u.ID)
	if err == errDemoDisabled {
		http.Error(w, "Demo credits are disabled", 403)
		return
	}
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	jsonResponse(w, map[string]any{"balance": balance, "added": 1000})
}
func (h *Handler) DemoSpend(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	u, _ := userFromContext(r.Context())
	balance, err := h.s.SpendDemo(r.Context(), u.ID)
	if err != nil {
		if !errors.Is(err, errInsufficientBalance) {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		http.Error(w, "Insufficient balance", 400)
		return
	}
	jsonResponse(w, map[string]any{"balance": balance, "spent": 250})
}
func (h *Handler) ShopItems(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	items, err := h.s.repo.ShopItems(r.Context())
	if err != nil {
		http.Error(w, "Database error", 500)
		return
	}
	jsonResponse(w, items)
}

func (h *Handler) ShopInventory(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	u, _ := userFromContext(r.Context())
	items, err := h.s.repo.Inventory(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, items)
}
func (h *Handler) ShopBuy(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	u, _ := userFromContext(r.Context())
	var req struct {
		ItemID string `json:"item_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || req.ItemID == "" {
		http.Error(w, "Invalid request", 400)
		return
	}
	balance, err := h.s.repo.Buy(r.Context(), u.ID, req.ItemID)
	if err != nil {
		if errors.Is(err, errItemNotFound) {
			http.Error(w, "Item not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, errAlreadyOwned) {
			http.Error(w, "Item already owned", http.StatusConflict)
			return
		}
		if errors.Is(err, errInsufficientBalance) {
			http.Error(w, "Insufficient balance", 400)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"status": "success", "item_id": req.ItemID, "balance": balance})
}
