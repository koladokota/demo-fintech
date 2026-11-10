package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestShopInventoryRequiresBearerToken(t *testing.T) {
	app := &App{}
	req := httptest.NewRequest(http.MethodGet, "/api/shop/inventory", nil)
	res := httptest.NewRecorder()
	app.shopInventory(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestShopInventoryReturnsPurchasedItem(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is required for the PostgreSQL integration test")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	userID := "test-user-" + time.Now().Format("20060102150405.000000000")
	itemID := "test-item-" + time.Now().Format("20060102150405.000000000")
	token := "test-token-" + userID
	_, err = db.Exec(ctx, `INSERT INTO users (id, name, email, password_hash) VALUES ($1, 'Inventory Test', $2, 'test')`, userID, userID+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Remove children before parents so cleanup also works if the test fails.
		_, _ = db.Exec(ctx, `DELETE FROM user_inventory WHERE user_id = $1`, userID)
		_, _ = db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		_, _ = db.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = db.Exec(ctx, `DELETE FROM shop_items WHERE id = $1`, itemID)
	})
	_, err = db.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, NOW() + interval '1 hour')`, hashToken(token), userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO shop_items (id, name, description, price, rarity) VALUES ($1, 'Test item', 'Test description', 1, 'rare')`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO user_inventory (user_id, item_id) VALUES ($1, $2)`, userID, itemID)
	if err != nil {
		t.Fatal(err)
	}

	app := &App{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/shop/inventory", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	app.shopInventory(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var items []struct {
		ID          string    `json:"id"`
		Rarity      string    `json:"rarity"`
		PurchasedAt time.Time `json:"purchased_at"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != itemID || items[0].Rarity != "rare" || items[0].PurchasedAt.IsZero() {
		t.Fatalf("unexpected inventory response: %+v", items)
	}
}
