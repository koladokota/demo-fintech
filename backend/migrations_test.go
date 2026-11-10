package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunMigrationsEmptyExistingAndRepeatedDatabase(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is required")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := *parsed
	admin.Path = "/postgres"
	adminPool, err := pgxpool.New(context.Background(), admin.String())
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	dbName := "migration_test_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, err = adminPool.Exec(context.Background(), "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminPool.Exec(context.Background(), "DROP DATABASE "+dbName) })
	testDB := *parsed
	testDB.Path = "/" + dbName
	db, err := pgxpool.New(context.Background(), testDB.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, file, _, _ := runtime.Caller(0)
	oldDir := os.Getenv("MIGRATIONS_DIR")
	t.Cleanup(func() { _ = os.Setenv("MIGRATIONS_DIR", oldDir) })
	_ = os.Setenv("MIGRATIONS_DIR", filepath.Join(filepath.Dir(file), "migrations"))
	if err := runMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO users(id,name,email,password_hash) VALUES('migration-sentinel','Sentinel','sentinel@example.test','test')`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- runMigrations(context.Background(), db) }()
	go func() { results <- runMigrations(context.Background(), db) }()
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations WHERE version='001_initial.sql'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration count = %d, want 1", count)
	}
	var sentinel int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE id='migration-sentinel'`).Scan(&sentinel); err != nil {
		t.Fatal(err)
	}
	if sentinel != 1 {
		t.Fatal(fmt.Sprintf("sentinel count = %d, want 1", sentinel))
	}
}
