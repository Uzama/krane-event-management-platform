// Command agent is the thin read-only CLI agent (FEATURES.md item 15): a
// small tool-use loop that calls a model with 3-4 read-only tools hitting
// the real public API as a real user, inheriting that user's authz. It is
// a separate binary under cmd/, not part of the layered core (CLAUDE.md).
//
// Usage:
//
//	KRANE_TOKEN=$(make token USER=admin) ANTHROPIC_API_KEY=... \
//	  go run ./cmd/agent -scenario=1
//
// Run `make token USER=admin|contributor|attendee` for a bearer token, and
// `make seed` for event/room/session ids to pass to -event-id and
// -foreign-event-id.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}

func run() error {
	scenario := flag.Int("scenario", 0, "which scripted scenario to run: 1 (normal read), 2 (permission boundary), 3 (composition)")
	token := flag.String("token", os.Getenv("KRANE_TOKEN"), "bearer token to authenticate as (default: $KRANE_TOKEN)")
	baseURL := flag.String("base-url", "http://localhost:8080", "Krane API base URL")
	anthropicKey := flag.String("anthropic-key", os.Getenv("ANTHROPIC_API_KEY"), "Anthropic API key (default: $ANTHROPIC_API_KEY)")
	model := flag.String("model", envOr("ANTHROPIC_MODEL", "claude-sonnet-5"), "Anthropic model id")
	eventID := flag.String("event-id", "", "event id used by scenario 1's default and scenario 3 (composition)")
	foreignEventID := flag.String("foreign-event-id", "", "an event id the caller is NOT a member of, for scenario 2 (permission boundary)")
	localDate := flag.String("local-date", "", "local date (YYYY-MM-DD) for scenario 3; defaults to the event's own start date")
	flag.Parse()

	if *scenario < 1 || *scenario > 3 {
		return fmt.Errorf("-scenario must be 1, 2, or 3")
	}
	if *token == "" {
		return fmt.Errorf("no bearer token: pass -token or set KRANE_TOKEN (see `make token USER=...`)")
	}
	if *anthropicKey == "" {
		return fmt.Errorf("no Anthropic API key: pass -anthropic-key or set ANTHROPIC_API_KEY")
	}
	if *scenario == 2 && *foreignEventID == "" {
		return fmt.Errorf("-foreign-event-id is required for scenario 2")
	}
	if *scenario == 3 && *eventID == "" {
		return fmt.Errorf("-event-id is required for scenario 3")
	}

	logger := utils.NewLogger(utils.Load())
	apiClient := NewClient(*baseURL, *token)
	modelClient := NewAnthropicClient(*anthropicKey, *model)

	chosen := Scenarios(*eventID, *foreignEventID, *localDate)[*scenario-1]
	logger.Info("scenario_start", "scenario", chosen.Name, "prompt", chosen.Prompt)

	final, err := Run(context.Background(), RunDeps{
		Model:  modelClient,
		API:    apiClient,
		Logger: logger,
	}, systemPrompt, chosen.Prompt)
	if err != nil {
		return fmt.Errorf("running scenario %q: %w", chosen.Name, err)
	}

	logger.Info("scenario_done", "scenario", chosen.Name, "answer", final)
	fmt.Println(final)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
