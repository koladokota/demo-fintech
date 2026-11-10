package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net/http"
)

func main() {
	cfg := loadConfig()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	if err = runMigrations(ctx, db); err != nil {
		log.Fatal("database migration failed: ", err)
	}
	svc := &Service{repo: &Repository{db: db}, demoCredits: cfg.DemoCredits}
	h := &Handler{s: svc}
	auth := svc.RequireAuth
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("/api/register", h.Register)
	mux.HandleFunc("/api/login", h.Login)
	mux.Handle("/api/me", auth(http.HandlerFunc(h.Me)))
	mux.Handle("/api/wallet", auth(http.HandlerFunc(h.Wallet)))
	mux.Handle("/api/wallet/demo-add", auth(http.HandlerFunc(h.DemoAdd)))
	mux.Handle("/api/wallet/demo-spend", auth(http.HandlerFunc(h.DemoSpend)))
	mux.HandleFunc("/api/shop/items", h.ShopItems)
	mux.Handle("/api/shop/inventory", auth(http.HandlerFunc(h.ShopInventory)))
	mux.Handle("/api/shop/buy", auth(http.HandlerFunc(h.ShopBuy)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "../index.html")
	})
	log.Printf("Server started at http://localhost:%s", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, mux))
}
