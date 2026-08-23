package utils_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

func TestNewLogger_LevelFromConfig(t *testing.T) {
	cases := []struct {
		name     string
		logLevel string
		want     slog.Level
	}{
		{"debug enables debug", "debug", slog.LevelDebug},
		{"info enables info, not debug", "info", slog.LevelInfo},
		{"warn enables warn, not info", "warn", slog.LevelWarn},
		{"error enables error, not warn", "error", slog.LevelError},
		{"unknown level defaults to info", "nonsense", slog.LevelInfo},
		{"empty level defaults to info", "", slog.LevelInfo},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger := utils.NewLogger(utils.Config{LogLevel: tc.logLevel})

			if !logger.Enabled(context.Background(), tc.want) {
				t.Errorf("logger with LogLevel=%q should enable %s", tc.logLevel, tc.want)
			}

			if tc.want == slog.LevelDebug {
				return // nothing below debug to assert against
			}
			below := tc.want - 1
			if logger.Enabled(context.Background(), below) {
				t.Errorf("logger with LogLevel=%q should not enable a level below %s", tc.logLevel, tc.want)
			}
		})
	}
}

func TestNewLogger_ReturnsNonNil(t *testing.T) {
	if utils.NewLogger(utils.Config{}) == nil {
		t.Fatal("NewLogger returned nil")
	}
}
