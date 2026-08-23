package utils

import (
	"log/slog"
	"os"
)

// NewLogger builds the structured JSON logger every layer writes through.
// Level comes from cfg.LogLevel; an unrecognised or empty value defaults to
// info rather than failing boot over a typo'd env var.
func NewLogger(cfg Config) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)})
	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
