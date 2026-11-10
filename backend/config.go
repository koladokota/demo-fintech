package main

import "os"

type Config struct {
	DatabaseURL string
	DemoCredits bool
	Port        string
}

func loadConfig() Config {
	return Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		DemoCredits: os.Getenv("ENABLE_DEMO_CREDITS") == "true",
		Port:        envOr("PORT", "8080"),
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
