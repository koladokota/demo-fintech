package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Repository struct{ db *pgxpool.Pool }

func (r *Repository) CreateUser(ctx context.Context, id, name, email, passwordHash string) (User, error) {
	var u User
	err := r.db.QueryRow(ctx, `INSERT INTO users (id,name,email,password_hash) VALUES ($1,$2,$3,$4) RETURNING id,name,email,created_at`, id, name, email, passwordHash).Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
	return u, err
}
func (r *Repository) FindUserByEmail(ctx context.Context, email string) (User, string, error) {
	var u User
	var hash string
	err := r.db.QueryRow(ctx, `SELECT id,name,email,password_hash,created_at FROM users WHERE email=$1`, email).Scan(&u.ID, &u.Name, &u.Email, &hash, &u.CreatedAt)
	return u, hash, err
}
func (r *Repository) CreateSession(ctx context.Context, userID, tokenHash string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, tokenHash, userID, time.Now().Add(24*time.Hour))
	return err
}
func (r *Repository) UserByToken(ctx context.Context, tokenHash string) (User, error) {
	var u User
	err := r.db.QueryRow(ctx, `SELECT u.id,u.name,u.email,u.created_at FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token_hash=$1 AND s.expires_at>NOW()`, tokenHash).Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
	return u, err
}
func (r *Repository) Wallet(ctx context.Context, userID string) (int64, []Transaction, error) {
	var balance int64
	if err := r.db.QueryRow(ctx, `SELECT virtual_balance FROM users WHERE id=$1`, userID).Scan(&balance); err != nil {
		return 0, nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT id,amount,type,description,created_at FROM wallet_transactions WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	items := make([]Transaction, 0)
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Amount, &t.Type, &t.Description, &t.CreatedAt); err != nil {
			return 0, nil, err
		}
		items = append(items, t)
	}
	return balance, items, rows.Err()
}
func (r *Repository) ChangeBalance(ctx context.Context, userID string, amount int64, typ, description string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var balance int64
	if err = tx.QueryRow(ctx, `UPDATE users SET virtual_balance=virtual_balance+$1 WHERE id=$2 AND virtual_balance+$1>=0 RETURNING virtual_balance`, amount, userID).Scan(&balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errInsufficientBalance
		}
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(user_id,amount,type,description) VALUES($1,$2,$3,$4)`, userID, amount, typ, description); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return balance, nil
}
func (r *Repository) ShopItems(ctx context.Context) ([]ShopItem, error) {
	rows, err := r.db.Query(ctx, `SELECT id,name,description,price,rarity,icon FROM shop_items WHERE active=TRUE ORDER BY price ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ShopItem, 0)
	for rows.Next() {
		var i ShopItem
		if err := rows.Scan(&i.ID, &i.Name, &i.Description, &i.Price, &i.Rarity, &i.Icon); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (r *Repository) Inventory(ctx context.Context, userID string) ([]InventoryItem, error) {
	rows, err := r.db.Query(ctx, `SELECT i.id,i.name,i.description,i.rarity,i.icon,ui.purchased_at FROM user_inventory ui JOIN shop_items i ON i.id=ui.item_id WHERE ui.user_id=$1 ORDER BY ui.purchased_at DESC,ui.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]InventoryItem, 0)
	for rows.Next() {
		var item InventoryItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Rarity, &item.Icon, &item.PurchasedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *Repository) Buy(ctx context.Context, userID, itemID string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var price int64
	var name string
	if err = tx.QueryRow(ctx, `SELECT name,price FROM shop_items WHERE id=$1 AND active=TRUE`, itemID).Scan(&name, &price); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errItemNotFound
		}
		return 0, err
	}
	var inventoryID int64
	if err = tx.QueryRow(ctx, `INSERT INTO user_inventory(user_id,item_id) VALUES($1,$2) ON CONFLICT(user_id,item_id) DO NOTHING RETURNING id`, userID, itemID).Scan(&inventoryID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errAlreadyOwned
		}
		return 0, err
	}
	var balance int64
	if err = tx.QueryRow(ctx, `UPDATE users SET virtual_balance=virtual_balance-$1 WHERE id=$2 AND virtual_balance>=$1 RETURNING virtual_balance`, price, userID).Scan(&balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errInsufficientBalance
		}
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(user_id,amount,type,description) VALUES($1,$2,'shop-purchase',$3)`, userID, -price, "Покупка: "+name); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return balance, nil
}

var (
	errItemNotFound        = errors.New("shop item not found")
	errAlreadyOwned        = errors.New("shop item already owned")
	errInsufficientBalance = errors.New("insufficient balance")
)
