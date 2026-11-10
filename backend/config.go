package main

import "os"

type Config struct {
	DatabaseURL string
	DemoCredits bool
	Environment string
	Port        string
}

func loadConfig() Config {
	return Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Environment: envOr("APP_ENV", "production"),
		DemoCredits: os.Getenv("ENABLE_DEMO_CREDITS") == "true" && (os.Getenv("APP_ENV") == "development" || os.Getenv("APP_ENV") == "test"),
		Port:        envOr("PORT", "8080"),
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
