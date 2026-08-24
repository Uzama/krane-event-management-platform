package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// countingTracer implements pgx.QueryTracer, counting every query actually
// sent to Postgres over a pool -- the mechanism FEATURES.md item 18 names
// ("a query-counting test (pgx tracer)"). TraceQueryStart/End is the pair
// pgx invokes per query; only Start needs to count.
type countingTracer struct {
	queries atomic.Int64
}

func (c *countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.queries.Add(1)
	return ctx
}

func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// tracedTestPool is testPool (user_repo_test.go) plus a countingTracer --
// a separate pool from the shared one so this test's count isn't polluted
// by any other query running concurrently against the same *pgxpool.Pool
// from a parallel test in this package.
func tracedTestPool(t *testing.T) (*pgxpool.Pool, *countingTracer) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultTestDatabaseURL
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tracer := &countingTracer{}
	cfg.ConnConfig.Tracer = tracer

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v\n\nThe suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, tracer
}

// seedSessionsForQueryCountTest creates n sessions in their own room/event,
// each with a distinct speaker and a time slot staggered an hour apart so
// item 16's EXCLUDE constraints never fire. Built directly inside
// krane_test by this test (via the real repository, same database the
// rest of this package's tests use) -- deliberately NOT relying on `make
// seed`'s dev-database Large-Scale Fixture Event, which item 14's own
// review flagged as invisible to `make test` (a separate database
// entirely; see Makefile's POSTGRES_TEST_DB vs the dev POSTGRES_DB).
func seedSessionsForQueryCountTest(t *testing.T, pool *pgxpool.Pool, n int) string {
	t.Helper()
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("QC Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating room: %v", err)
	}
	repo := postgres.NewSessionRepository(pool)

	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < n; i++ {
		speaker := createTestUser(t, pool)
		starts := base.Add(time.Duration(i) * time.Hour)
		in := session.CreateInput{
			RoomID:    rm.ID,
			SpeakerID: speaker,
			Title:     fmt.Sprintf("QC session %d", i),
			StartsAt:  starts,
			EndsAt:    starts.Add(30 * time.Minute),
		}
		if _, err := repo.Create(ctx, creator, ev.ID, in); err != nil {
			t.Fatalf("Create session #%d: %v", i, err)
		}
	}
	return ev.ID
}

// TestSessionRepository_List_QueryCountIndependentOfResultSize is item
// 18's must: test. A List call's query count against Postgres must be
// identical whether the event has 5 sessions or 500 -- item 18's whole
// point is that room_name/speaker_name are batched via a single JOIN
// (session_repo.go's List), not fetched per row, so this asserts the
// counts for both sizes AND that the count itself is exactly 1 (the
// strongest form of "constant": not just "equal to each other" but
// "equal to the smallest number a correct implementation can produce").
func TestSessionRepository_List_QueryCountIndependentOfResultSize(t *testing.T) {
	small := func(t *testing.T) int64 {
		pool, tracer := tracedTestPool(t)
		eventID := seedSessionsForQueryCountTest(t, pool, 5)
		tracer.queries.Store(0) // only count List's own queries, not the fixture setup above

		repo := postgres.NewSessionRepository(pool)
		page, err := repo.List(context.Background(), eventID, 100, nil)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page.Sessions) != 5 {
			t.Fatalf("got %d sessions, want 5", len(page.Sessions))
		}
		for _, s := range page.Sessions {
			if s.RoomName == "" || s.SpeakerName == "" {
				t.Fatalf("session %q missing RoomName/SpeakerName -- the JOIN isn't populating them: %+v", s.ID, s)
			}
		}
		return tracer.queries.Load()
	}

	large := func(t *testing.T) int64 {
		pool, tracer := tracedTestPool(t)
		eventID := seedSessionsForQueryCountTest(t, pool, 500)
		tracer.queries.Store(0)

		repo := postgres.NewSessionRepository(pool)
		page, err := repo.List(context.Background(), eventID, 500, nil)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page.Sessions) != 500 {
			t.Fatalf("got %d sessions, want 500", len(page.Sessions))
		}
		for _, s := range page.Sessions {
			if s.RoomName == "" || s.SpeakerName == "" {
				t.Fatalf("session %q missing RoomName/SpeakerName -- the JOIN isn't populating them: %+v", s.ID, s)
			}
		}
		return tracer.queries.Load()
	}

	smallCount := small(t)
	largeCount := large(t)

	if smallCount != 1 {
		t.Fatalf("5-session List issued %d queries, want exactly 1 (a single JOIN)", smallCount)
	}
	if largeCount != 1 {
		t.Fatalf("500-session List issued %d queries, want exactly 1 (a single JOIN)", largeCount)
	}
	if smallCount != largeCount {
		t.Fatalf("query count depends on result size: 5 children -> %d queries, 500 children -> %d queries", smallCount, largeCount)
	}
}
