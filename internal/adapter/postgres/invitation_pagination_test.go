package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
)

// bulkInsertInvitations loads n invitation rows for eventID directly via
// COPY (pgx.CopyFrom), bypassing InvitationRepository.Create the same way
// cmd/seed's own load.go does -- fast enough to build a 50k-row fixture
// inside krane_test in under a second, matching cmd/seed's measured
// ~1.1s for its whole 50k-invitation dataset. id is omitted from the
// column list, so uuidv7() DEFAULT still applies (COPY honours column
// defaults for omitted columns); the row's own uuid is read back
// afterward via a scoped SELECT, not generated client-side, since nothing
// downstream needs it to be predictable.
//
// created_at is set explicitly, one millisecond apart starting at start,
// so the fixture has a real, strictly increasing keyset order to
// paginate over -- this test is not exercising the tie-break case
// (FAILURES.md's rule), just deep pagination under concurrent insertion.
func bulkInsertInvitations(t *testing.T, pool *pgxpool.Pool, eventID string, n int, start time.Time) {
	t.Helper()

	rows := make([][]any, n)
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration(i) * time.Millisecond)
		rows[i] = []any{eventID, fmt.Sprintf("%s-%d@example.com", uniqueSubject(t), i), "attendee", ts, ts}
	}

	_, err := pool.CopyFrom(context.Background(),
		pgx.Identifier{"invitations"},
		[]string{"event_id", "email", "role", "created_at", "updated_at"},
		pgx.CopyFromRows(rows))
	if err != nil {
		t.Fatalf("bulk-inserting %d invitations: %v", n, err)
	}
}

// TestInvitationRepository_List_DeepPaginationWithConcurrentInsertsAheadOfCursor
// is item 19's must: test: paginate deep into a large invitation set while
// inserting rows mid-traversal, asserting no skips or duplicates. Built
// entirely inside krane_test by this test (item 14's own review already
// flagged that make seed's dev-database dataset is invisible to make
// test) -- 50,000 rows via COPY, matching the scale FEATURES.md names.
//
// The inserted rows are deliberately synchronized to land AHEAD of the
// reader's current cursor position -- created_at values set just past the
// page the reader has already consumed, still inside the range of pages
// not yet fetched -- not merely "inserted at some point during the
// traversal" with no ordering relationship to the cursor. That is the
// only insertion pattern that actually exercises keyset correctness:
// inserting behind the cursor proves nothing (a keyset reader is never
// expected to see rows created before its snapshot point started); it is
// specifically a new row landing in not-yet-visited territory that a
// broken cursor (e.g. one that quietly reverts to OFFSET semantics, or
// double-counts on a boundary) could skip or duplicate.
func TestInvitationRepository_List_DeepPaginationWithConcurrentInsertsAheadOfCursor(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	const totalSeeded = 50_000
	const pageSize = 500
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bulkInsertInvitations(t, pool, ev.ID, totalSeeded, base)

	seededIDs := listAllInvitationIDs(t, pool, ev.ID)
	if len(seededIDs) != totalSeeded {
		t.Fatalf("seeded %d invitations, but only found %d rows -- fixture setup didn't take", totalSeeded, len(seededIDs))
	}
	want := make(map[string]bool, totalSeeded)
	for _, id := range seededIDs {
		want[id] = true
	}

	// Insert 200 more rows once traversal has reached roughly the halfway
	// point, timestamped to land immediately after the cursor the reader
	// is currently sitting at -- ahead of it, inside not-yet-fetched
	// territory, never behind it.
	const injectAfterPage = (totalSeeded / pageSize) / 2
	const injected = 200

	seen := make(map[string]bool, totalSeeded+injected)
	var cursor *invitation.Cursor
	pageNum := 0
	injectedAt := time.Time{}

	for {
		page, err := repo.List(ctx, ev.ID, pageSize, cursor)
		if err != nil {
			t.Fatalf("List page %d: %v", pageNum, err)
		}
		for _, inv := range page.Invitations {
			if seen[inv.ID] {
				t.Fatalf("duplicate: invitation %q seen twice (page %d)", inv.ID, pageNum)
			}
			seen[inv.ID] = true
		}

		pageNum++

		if pageNum == injectAfterPage && page.NextCursor != nil {
			// page.NextCursor -- not the `cursor` variable used to fetch
			// this page -- is the reader's position AFTER this page: the
			// last row just consumed. Using the pre-fetch `cursor` here
			// would place the injected rows BEHIND already-visited
			// territory (a bug caught by this test failing on the first
			// run, before this fix), not ahead of it.
			injectedAt = page.NextCursor.CreatedAt.Add(time.Microsecond)
			bulkInsertInvitations(t, pool, ev.ID, injected, injectedAt)
			ids := listAllInvitationIDs(t, pool, ev.ID)
			for _, id := range ids {
				if !want[id] {
					want[id] = true // the newly-injected ones
				}
			}
		}

		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}

	if injectedAt.IsZero() {
		t.Fatal("test setup bug: injection point was never reached (injectAfterPage too large for the number of pages traversed)")
	}
	if len(want) != totalSeeded+injected {
		t.Fatalf("want set has %d entries, expected %d (%d seeded + %d injected)", len(want), totalSeeded+injected, totalSeeded, injected)
	}
	if len(seen) != len(want) {
		t.Fatalf("traversal visited %d distinct invitations, want %d -- a skip or a miscount", len(seen), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("invitation %q was never visited (a skip)", id)
		}
	}
}

func listAllInvitationIDs(t *testing.T, pool *pgxpool.Pool, eventID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM invitations WHERE event_id = $1`, eventID)
	if err != nil {
		t.Fatalf("listing invitation ids: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning invitation id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing invitation ids: %v", err)
	}
	return ids
}
