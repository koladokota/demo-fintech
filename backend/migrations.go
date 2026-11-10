package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runMigrations(ctx context.Context, db *pgxpool.Pool) error {
	path := os.Getenv("MIGRATIONS_DIR")
	if path == "" {
		path = "migrations"
	}
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(path, file))
		if err != nil {
			return err
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		// Serialize migration runners across all application processes.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('demo-fintech:migrations', 0))`); err == nil {
			var applied bool
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, file).Scan(&applied)
			if err == nil && applied {
				err = tx.Commit(ctx)
				if err == nil {
					continue
				}
			}
		}
		if err == nil {
			_, err = tx.Exec(ctx, string(data))
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, file)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", file, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration %s commit: %w", file, err)
		}
	}
	return nil
}
