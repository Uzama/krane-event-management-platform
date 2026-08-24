// Command seed loads demo/scale data into the dev database (FEATURES.md item
// 14): 50 events, 5,000 users, 50,000 invitations, spanning multiple IANA
// time zones with two events whose sessions cross a DST boundary. See
// generate.go for what gets built, load.go for how it reaches Postgres, and
// demo.go for the three identities `make token USER=...` resolves against.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// defaultSeedDatabaseURL is krane_migrator against the dev database -- see
// load.go's Truncate for why the migrator role, not krane_app, is required.
const defaultSeedDatabaseURL = "postgres://krane_migrator:dev_only_migrator@localhost:5432/krane?sslmode=disable"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	start := time.Now()
	cfg := utils.Load()

	if cfg.Env == "production" {
		return fmt.Errorf("refusing to run: ENV=production -- cmd/seed truncates and reloads fixture data; never point it at a real database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := utils.NewPool(ctx, seedDatabaseURL())
	if err != nil {
		return fmt.Errorf("connecting as krane_migrator: %w", err)
	}
	defer pool.Close()

	if err := Truncate(ctx, pool); err != nil {
		return fmt.Errorf("truncating existing seed data: %w", err)
	}

	ds := GenerateDataset(DefaultConfig(), DefaultSeed)
	if err := Load(ctx, pool, ds); err != nil {
		return fmt.Errorf("loading generated dataset: %w", err)
	}

	if err := selfCheckDemoIdentities(ctx, cfg, pool); err != nil {
		return fmt.Errorf("self-check: %w", err)
	}

	fmt.Printf("seed: loaded %d events, %d users, %d invitations, %d rooms, %d sessions, %d memberships in %s\n",
		len(ds.Events), len(ds.Users), len(ds.Invitations), len(ds.Rooms), len(ds.Sessions), len(ds.Members),
		time.Since(start).Round(time.Millisecond))
	fmt.Printf("seed: demo identities verified against the real mock OIDC issuer on %q -- try `make token USER=admin|contributor|attendee`\n", seedDemoEventName)
	return nil
}

func seedDatabaseURL() string {
	if v := os.Getenv("SEED_DATABASE_URL"); v != "" {
		return v
	}
	return defaultSeedDatabaseURL
}

// selfCheckDemoIdentities is decision 4 of the feature plan's real check,
// not a check against seed's own writes: it mints a real token from the
// mock OIDC issuer for each of the 3 demo identities (the same way `make
// token USER=...` does), verifies it with the exact auth.Verifier the
// production middleware uses, and only then looks the resulting subject up
// in the database. That's what catches a drift between cmd/seed/demo.go and
// docker-compose.yml's JSON_CONFIG -- a check that just re-read seed's own
// inserted rows could never catch that, since it would find its own
// (possibly wrong) subject and call it a match.
func selfCheckDemoIdentities(ctx context.Context, cfg utils.Config, pool *pgxpool.Pool) error {
	verifier, err := auth.New(ctx, cfg.OIDCIssuerURL, cfg.OIDCAudience)
	if err != nil {
		return fmt.Errorf("building an auth verifier against %q: %w", cfg.OIDCIssuerURL, err)
	}

	for _, want := range demoIdentities {
		token, err := mintDemoToken(ctx, cfg.OIDCIssuerURL, want.clientID)
		if err != nil {
			return fmt.Errorf("minting a token for client_id %q: %w", want.clientID, err)
		}

		claims, err := verifier.Verify(ctx, token)
		if err != nil {
			return fmt.Errorf("verifying the token minted for client_id %q: %w", want.clientID, err)
		}

		var userID, email, name string
		err = pool.QueryRow(ctx, `SELECT id, email, name FROM users WHERE subject = $1`, claims.Subject).
			Scan(&userID, &email, &name)
		if err != nil {
			return fmt.Errorf(
				"client_id %q minted a token for subject %q, but no seeded user has that subject -- "+
					"cmd/seed/demo.go and docker-compose.yml's oidc JSON_CONFIG have drifted: %w",
				want.clientID, claims.Subject, err,
			)
		}
		if email != want.email || name != want.name {
			return fmt.Errorf("subject %q: seeded user has email/name %q/%q, want %q/%q", claims.Subject, email, name, want.email, want.name)
		}

		var role string
		err = pool.QueryRow(ctx, `
			SELECT em.role
			FROM event_members em
			JOIN events e ON e.id = em.event_id
			WHERE em.user_id = $1 AND e.name = $2`, userID, seedDemoEventName).
			Scan(&role)
		if err != nil {
			return fmt.Errorf("subject %q has no event_members row on %q: %w", claims.Subject, seedDemoEventName, err)
		}
		if role != want.role {
			return fmt.Errorf("subject %q on %q: got role %q, want %q", claims.Subject, seedDemoEventName, role, want.role)
		}
	}
	return nil
}

// mintDemoToken POSTs a client_credentials token request to the mock
// issuer's token endpoint, exactly as the Makefile's `token` target does
// (docker-compose.yml's oidc JSON_CONFIG maps client_id demo-admin/
// demo-contributor/demo-attendee to fixed claims).
func mintDemoToken(ctx context.Context, issuerURL, clientID string) (string, error) {
	form := "grant_type=client_credentials&client_secret=unused&client_id=" + clientID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuerURL+"/token", strings.NewReader(form))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("token response had no access_token: %s", body)
	}
	return payload.AccessToken, nil
}
