package main

import "time"

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
type Transaction struct {
	ID          int64     `json:"id"`
	Amount      int64     `json:"amount"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}
type ShopItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Rarity      string `json:"rarity"`
	Icon        string `json:"icon"`
}
type InventoryItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Rarity      string    `json:"rarity"`
	Icon        string    `json:"icon"`
	PurchasedAt time.Time `json:"purchased_at"`
}
