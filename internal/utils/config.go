// Package utils holds leaf-only helpers: config, logging, and the db pool
// constructor. Any layer may import it; it imports nothing inner.
package utils

import "os"

// Config is the typed, env-derived configuration every entrypoint boots from.
type Config struct {
	Env         string
	HTTPPort    string
	DatabaseURL string
	LogLevel    string
}

// Load reads Config from the environment, falling back to the same dev
// defaults docker-compose.yml and the Makefile already use, so a clean clone
// with no .env runs `go run ./cmd/api` against `make up`'s stack untouched.
func Load() Config {
	return Config{
		Env:         getEnv("ENV", "development"),
		HTTPPort:    getEnv("HTTP_PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://krane_app:dev_only_app@localhost:5432/krane?sslmode=disable"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
