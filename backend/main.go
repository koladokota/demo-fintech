package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type App struct {
	db *pgxpool.Pool
}

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type AuthRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *App) initDB(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		virtual_balance BIGINT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at TIMESTAMPTZ NOT NULL
	);
	CREATE TABLE IF NOT EXISTS wallet_transactions (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount BIGINT NOT NULL CHECK (amount <> 0),
    type TEXT NOT NULL,
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_wallet_transactions_user
	ON wallet_transactions(user_id, created_at DESC);
	CREATE TABLE IF NOT EXISTS shop_items (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    price BIGINT NOT NULL CHECK (price >= 0),
    rarity TEXT NOT NULL CHECK (
        rarity IN ('common', 'rare', 'epic', 'legendary')
    ),
    icon TEXT NOT NULL DEFAULT 'package',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);


	CREATE TABLE IF NOT EXISTS user_inventory (
	id BIGSERIAL PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	item_id TEXT NOT NULL REFERENCES shop_items(id),
	purchased_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (user_id, item_id)
	);

	CREATE INDEX IF NOT EXISTS idx_user_inventory_user
	ON user_inventory(user_id, purchased_at DESC);
	`
	_, err := a.db.Exec(ctx, query)
	return err
}

func (a *App) createSession(ctx context.Context, userID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}

	_, err = a.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at)
		 VALUES ($1, $2, $3)`,
		hashToken(token), userID, time.Now().Add(24*time.Hour),
	)
	return token, err
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if req.Name == "" || !strings.Contains(req.Email, "@") || len(req.Password) < 8 {
		http.Error(w, "Invalid name, email or password (minimum 8 characters)", http.StatusBadRequest)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Password processing failed", http.StatusInternalServerError)
		return
	}

	id, err := randomToken()
	if err != nil {
		http.Error(w, "ID generation failed", http.StatusInternalServerError)
		return
	}

	var user User
	err = a.db.QueryRow(r.Context(),
		`INSERT INTO users (id, name, email, password_hash)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, name, email, created_at`,
		id, req.Name, req.Email, string(passwordHash),
	).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)

	if err != nil {
		http.Error(w, "Email may already be registered", http.StatusConflict)
		return
	}

	token, err := a.createSession(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Session creation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AuthResponse{Token: token, User: user})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	var user User
	var passwordHash string

	err := a.db.QueryRow(r.Context(),
		`SELECT id, name, email, password_hash, created_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Name, &user.Email, &passwordHash, &user.CreatedAt)

	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}

	token, err := a.createSession(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Session creation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AuthResponse{Token: token, User: user})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token := strings.TrimPrefix(header, "Bearer ")
	var user User

	err := a.db.QueryRow(r.Context(),
		`SELECT u.id, u.name, u.email, u.created_at
		 FROM users u
		 JOIN sessions s ON s.user_id = u.id
		 WHERE s.token_hash = $1 AND s.expires_at > NOW()`,
		hashToken(token),
	).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (a *App) wallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	var userID string
	var balance int64

	err := a.db.QueryRow(ctx, `
		SELECT u.id, u.virtual_balance
		FROM users u
		JOIN sessions s ON s.user_id = u.id
		WHERE s.token_hash = $1 AND s.expires_at > NOW()
	`, hashToken(token)).Scan(&userID, &balance)

	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := a.db.Query(ctx, `
		SELECT id, amount, type, description, created_at
		FROM wallet_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 100
	`, userID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Transaction struct {
		ID          int64     `json:"id"`
		Amount      int64     `json:"amount"`
		Type        string    `json:"type"`
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"created_at"`
	}

	transactions := make([]Transaction, 0)

	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.Amount, &t.Type,
			&t.Description, &t.CreatedAt,
		); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		transactions = append(transactions, t)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"balance":      balance,
		"transactions": transactions,
	})
}

func (a *App) demoAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	tx, err := a.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	var userID string
	var balance int64

	err = tx.QueryRow(ctx, `
		UPDATE users
		SET virtual_balance = virtual_balance + 1000
		WHERE id = (
			SELECT user_id
			FROM sessions
			WHERE token_hash = $1 AND expires_at > NOW()
		)
		RETURNING id, virtual_balance
	`, hashToken(token)).Scan(&userID, &balance)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_transactions
		(user_id, amount, type, description)
		VALUES ($1, 1000, 'demo-add', 'Тестовое начисление')
	`, userID)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"balance": balance,
		"added":   1000,
	})
}

func (a *App) demoSpend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	tx, err := a.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	var userID string
	var balance int64

	err = tx.QueryRow(ctx, `
		UPDATE users
		SET virtual_balance = virtual_balance - 250
		WHERE id = (
			SELECT user_id
			FROM sessions
			WHERE token_hash = $1 AND expires_at > NOW()
		)
		AND virtual_balance >= 250
		RETURNING id, virtual_balance
	`, hashToken(token)).Scan(&userID, &balance)

	if errors.Is(err, pgx.ErrNoRows) {
		var validSession bool
		checkErr := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM sessions
				WHERE token_hash = $1 AND expires_at > NOW()
			)
		`, hashToken(token)).Scan(&validSession)

		if checkErr != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
		} else if !validSession {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "Insufficient balance", http.StatusBadRequest)
		}
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_transactions
		(user_id, amount, type, description)
		VALUES ($1, -250, 'demo-spend', 'Тестовое списание')
	`, userID)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"balance": balance,
		"spent":   250,
	})
}

func (a *App) shopItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type ShopItem struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Price       int64  `json:"price"`
		Rarity      string `json:"rarity"`
		Icon        string `json:"icon"`
	}

	rows, err := a.db.Query(r.Context(), `
		SELECT id, name, description, price, rarity, icon
		FROM shop_items
		WHERE active = TRUE
		ORDER BY price ASC
	`)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]ShopItem, 0)

	for rows.Next() {
		var item ShopItem

		err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Description,
			&item.Price,
			&item.Rarity,
			&item.Icon,
		)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (a *App) shopInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type InventoryItem struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Description string    `json:"description"`
		Rarity      string    `json:"rarity"`
		Icon        string    `json:"icon"`
		PurchasedAt time.Time `json:"purchased_at"`
	}

	rows, err := a.db.Query(r.Context(), `
		SELECT i.id, i.name, i.description, i.rarity, i.icon, ui.purchased_at
		FROM user_inventory ui
		JOIN shop_items i ON i.id = ui.item_id
		WHERE ui.user_id = (
			SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at > NOW() LIMIT 1
		)
		ORDER BY ui.purchased_at DESC, ui.id DESC
	`, hashToken(strings.TrimPrefix(auth, "Bearer ")))
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]InventoryItem, 0)
	for rows.Next() {
		var item InventoryItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Rarity, &item.Icon, &item.PurchasedAt); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// An invalid or expired token must not look like an empty inventory.
	var valid bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM sessions WHERE token_hash = $1 AND expires_at > NOW())`, hashToken(strings.TrimPrefix(auth, "Bearer "))).Scan(&valid); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if !valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (a *App) shopBuy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ItemID string `json:"item_id"`
	}

	err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, 1024),
	).Decode(&req)

	if err != nil || strings.TrimSpace(req.ItemID) == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	tx, err := a.db.Begin(ctx)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	var userID string

	err = tx.QueryRow(ctx, `
		SELECT user_id
		FROM sessions
		WHERE token_hash = $1 AND expires_at > NOW()
	`, hashToken(token)).Scan(&userID)

	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var price int64
	var itemName string

	err = tx.QueryRow(ctx, `
		SELECT name, price
		FROM shop_items
		WHERE id = $1 AND active = TRUE
	`, req.ItemID).Scan(&itemName, &price)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Item not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	var inventoryID int64

	err = tx.QueryRow(ctx, `
		INSERT INTO user_inventory (user_id, item_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, item_id) DO NOTHING
		RETURNING id
	`, userID, req.ItemID).Scan(&inventoryID)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Item already owned", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	var balance int64

	err = tx.QueryRow(ctx, `
		UPDATE users
		SET virtual_balance = virtual_balance - $1
		WHERE id = $2 AND virtual_balance >= $1
		RETURNING virtual_balance
	`, price, userID).Scan(&balance)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Insufficient balance", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_transactions
		(user_id, amount, type, description)
		VALUES ($1, $2, 'shop-purchase', $3)
	`, userID, -price, "Покупка: "+itemName)

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"item_id": req.ItemID,
		"balance": balance,
	})
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	ctx := context.Background()

	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal("Database connection error: ", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatal("Database ping failed: ", err)
	}

	app := &App{db: db}
	if err := app.initDB(ctx); err != nil {
		log.Fatal("Database initialization failed: ", err)
	}

	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	http.HandleFunc("/api/register", app.register)
	http.HandleFunc("/api/login", app.login)
	http.HandleFunc("/api/me", app.me)
	http.HandleFunc("/api/wallet", app.wallet)
	http.HandleFunc("/api/wallet/demo-add", app.demoAdd)
	http.HandleFunc("/api/wallet/demo-spend", app.demoSpend)
	http.HandleFunc("/api/shop/items", app.shopItems)
	http.HandleFunc("/api/shop/inventory", app.shopInventory)
	http.HandleFunc("/api/shop/buy", app.shopBuy)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "../index.html")
	})

	log.Println("Server started at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
