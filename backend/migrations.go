package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"path/filepath"
)

func runMigrations(ctx context.Context, db *pgxpool.Pool) error {
	path := os.Getenv("MIGRATIONS_DIR")
	if path == "" {
		path = "migrations"
	}
	data, err := os.ReadFile(filepath.Join(path, "001_initial.sql"))
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, string(data))
	return err
}
