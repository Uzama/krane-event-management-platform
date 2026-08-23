package utils_test

import (
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// clearConfigEnv resets every env var Load reads, so a developer's shell
// environment (or a previous subtest) can't leak into the assertions below.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ENV", "HTTP_PORT", "DATABASE_URL", "LOG_LEVEL"} {
		t.Setenv(key, "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearConfigEnv(t)

	cfg := utils.Load()

	want := utils.Config{
		Env:         "development",
		HTTPPort:    "8080",
		DatabaseURL: "postgres://krane_app:dev_only_app@localhost:5432/krane?sslmode=disable",
		LogLevel:    "info",
	}
	if cfg != want {
		t.Fatalf("Load() with no env set = %+v, want %+v", cfg, want)
	}
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	clearConfigEnv(t)

	t.Setenv("ENV", "production")
	t.Setenv("HTTP_PORT", "9999")
	t.Setenv("DATABASE_URL", "postgres://custom:pw@db:5432/krane?sslmode=disable")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := utils.Load()

	want := utils.Config{
		Env:         "production",
		HTTPPort:    "9999",
		DatabaseURL: "postgres://custom:pw@db:5432/krane?sslmode=disable",
		LogLevel:    "debug",
	}
	if cfg != want {
		t.Fatalf("Load() with env overrides = %+v, want %+v", cfg, want)
	}
}
