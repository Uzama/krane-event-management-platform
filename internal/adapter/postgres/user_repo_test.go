package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
)

// Matches the Makefile's TEST_DATABASE_URL default (see migrations/schema_test.go
// and internal/container/container_test.go).
const defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultTestDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v\n\nThe suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

// uniqueSubject gives every test its own subject so parallel packages never
// collide on users.subject's unique constraint -- CLAUDE.md: "No test may
// assume it is the only occupant of the database."
func uniqueSubject(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

func TestUserRepository_GetOrCreateBySubject_CreatesOnFirstSignIn(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewUserRepository(pool)
	ctx := context.Background()

	subject := uniqueSubject(t)

	got, err := repo.GetOrCreateBySubject(ctx, subject, subject+"@test.krane", "Test User")
	if err != nil {
		t.Fatalf("GetOrCreateBySubject: %v", err)
	}

	if got.ID == "" {
		t.Error("got empty ID, want a generated uuidv7")
	}
	if got.Subject != subject {
		t.Errorf("got Subject %q, want %q", got.Subject, subject)
	}
	if got.Email != subject+"@test.krane" {
		t.Errorf("got Email %q, want %q", got.Email, subject+"@test.krane")
	}
	if got.Name != "Test User" {
		t.Errorf("got Name %q, want %q", got.Name, "Test User")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("got zero CreatedAt/UpdatedAt, want them populated")
	}
}

func TestUserRepository_GetOrCreateBySubject_ReusesExistingRowOnSecondSignIn(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewUserRepository(pool)
	ctx := context.Background()

	subject := uniqueSubject(t)

	first, err := repo.GetOrCreateBySubject(ctx, subject, subject+"@test.krane", "Test User")
	if err != nil {
		t.Fatalf("first GetOrCreateBySubject: %v", err)
	}

	// A different email/name on the second sign-in must NOT change the
	// stored row -- profile fields are captured once, at creation, and
	// never refreshed (see the plan's explicit decision on this).
	second, err := repo.GetOrCreateBySubject(ctx, subject, "changed@test.krane", "Changed Name")
	if err != nil {
		t.Fatalf("second GetOrCreateBySubject: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("second sign-in got a different id (%q) than the first (%q); want the same row reused", second.ID, first.ID)
	}
	if second.Email != first.Email {
		t.Errorf("second sign-in changed Email to %q; want it to stay %q (captured once at creation)", second.Email, first.Email)
	}
	if second.Name != first.Name {
		t.Errorf("second sign-in changed Name to %q; want it to stay %q (captured once at creation)", second.Name, first.Name)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE subject = $1`, subject).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows for subject %q after two sign-ins, want exactly 1", count, subject)
	}
}
