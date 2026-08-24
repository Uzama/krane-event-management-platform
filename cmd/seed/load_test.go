package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Matches the Makefile's TEST_SEED_DATABASE_URL default. Two things set
// this apart from every other package's testPool: krane_migrator, not
// krane_app (Truncate needs DELETE rights on audit_log the runtime role
// deliberately doesn't have -- decision 5 of the feature plan), and its own
// database (cmd_seed, not krane_test) -- Truncate does real full-table
// DELETEs, which would race every other package's fixtures if it ran
// against the database `go test ./...` shares across packages. The
// Makefile's `test` target creates cmd_seed as `CREATE DATABASE cmd_seed
// TEMPLATE krane_test` right after migrating krane_test, matching
// CLAUDE.md's own anticipated per-package escape hatch.
const defaultTestSeedDatabaseURL = "postgres://krane_migrator:dev_only_migrator@localhost:5432/cmd_seed?sslmode=disable"

func testMigratorPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_SEED_DATABASE_URL")
	if url == "" {
		url = defaultTestSeedDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v\n\nThe suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database as krane_migrator: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

// smallConfig keeps load_test.go fast -- proving Truncate/Load correctness
// doesn't need the full 50k-row scale; that's exercised for real by `make
// seed` itself against the dev database (see the feature plan).
func smallConfig() Config {
	return Config{
		UserCount:              200,
		BulkEventCount:         2,
		InvitationsPerEvent:    20,
		MinRoomsPerEvent:       2,
		MaxRoomsPerEvent:       3,
		MinSessionsPerEvent:    3,
		MaxSessionsPerEvent:    5,
		LargeEventRoomCount:    2,
		LargeEventSessionCount: 6,
	}
}

func rowCounts(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	ctx := context.Background()
	tables := []string{"users", "events", "event_members", "rooms", "sessions", "invitations"}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

func TestTruncateAndLoad_RowCountsMatchDataset(t *testing.T) {
	pool := testMigratorPool(t)
	ctx := context.Background()

	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("Truncate (starting from a clean slate): %v", err)
	}

	ds := GenerateDataset(smallConfig(), DefaultSeed)
	if err := Load(ctx, pool, ds); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := rowCounts(t, pool)
	want := map[string]int{
		"users":         len(ds.Users),
		"events":        len(ds.Events),
		"event_members": len(ds.Members),
		"rooms":         len(ds.Rooms),
		"sessions":      len(ds.Sessions),
		"invitations":   len(ds.Invitations),
	}
	for table, n := range want {
		if got[table] != n {
			t.Errorf("table %s: got %d rows, want %d", table, got[table], n)
		}
	}
}

func TestTruncateAndLoad_RunTwiceIsIdempotent(t *testing.T) {
	pool := testMigratorPool(t)
	ctx := context.Background()

	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("initial Truncate: %v", err)
	}
	firstDS := GenerateDataset(smallConfig(), DefaultSeed)
	if err := Load(ctx, pool, firstDS); err != nil {
		t.Fatalf("first Load: %v", err)
	}

	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("second Truncate: %v", err)
	}
	secondDS := GenerateDataset(smallConfig(), DefaultSeed+1)
	if err := Load(ctx, pool, secondDS); err != nil {
		t.Fatalf("second Load: %v", err)
	}

	got := rowCounts(t, pool)
	if got["users"] != len(secondDS.Users) {
		t.Errorf("got %d users after re-seed, want %d (first run's rows must be gone, not doubled)", got["users"], len(secondDS.Users))
	}
	if got["invitations"] != len(secondDS.Invitations) {
		t.Errorf("got %d invitations after re-seed, want %d", got["invitations"], len(secondDS.Invitations))
	}
}

// TestTruncate_ClearsAuditLogRowsReferencingSeededUsers is the exact
// scenario decision 5 of the feature plan exists for: `make token
// USER=admin` hits the real API, which writes a real audit_log row whose
// actor_id is a seeded user. krane_app has no DELETE grant on audit_log at
// all (item 02, append-only) -- as krane_app, re-seeding after that could
// never clear it and DELETE FROM users would fail its FK. This proves
// Truncate, running as krane_migrator with the scoped
// `WHERE actor_id IN (SELECT id FROM users)` cleanup, resolves it.
func TestTruncate_ClearsAuditLogRowsReferencingSeededUsers(t *testing.T) {
	pool := testMigratorPool(t)
	ctx := context.Background()

	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("initial Truncate: %v", err)
	}
	ds := GenerateDataset(smallConfig(), DefaultSeed)
	if err := Load(ctx, pool, ds); err != nil {
		t.Fatalf("Load: %v", err)
	}

	actor := ds.Users[0].ID
	entity := ds.Events[0].ID
	_, err := pool.Exec(ctx,
		`INSERT INTO audit_log (actor_id, entity_type, entity_id, action, after) VALUES ($1, 'event', $2, 'create', '{}'::jsonb)`,
		actor, entity,
	)
	if err != nil {
		t.Fatalf("simulating real API activity (inserting an audit_log row): %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO idempotency_keys (actor_id, key, endpoint, request_hash, response_status) VALUES ($1, 'k', '/v1/events', 'h', 201)`,
		actor,
	)
	if err != nil {
		t.Fatalf("simulating real API activity (inserting an idempotency_keys row): %v", err)
	}

	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("Truncate after simulated API activity: %v (this is the bug decision 5 fixes -- krane_app would fail here with a foreign key violation on users)", err)
	}

	got := rowCounts(t, pool)
	if got["users"] != 0 {
		t.Errorf("got %d users after Truncate, want 0", got["users"])
	}
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_log").Scan(&auditCount); err != nil {
		t.Fatalf("counting audit_log: %v", err)
	}
	if auditCount != 0 {
		t.Errorf("got %d audit_log rows after Truncate, want 0 (the simulated row referenced a user this run deleted)", auditCount)
	}
}
