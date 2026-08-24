package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// TestSessionRepository_CreateSeries_MaterializesEachOccurrenceAsAnOrdinarySession
// is item 23's core proof: every occurrence is a real sessions row, tagged
// with series_id, staggered by the recurrence rule -- never a parallel
// scheduling structure.
func TestSessionRepository_CreateSeries_MaterializesEachOccurrenceAsAnOrdinarySession(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Series Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating room: %v", err)
	}

	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC) // a Monday
	in := session.SeriesCreateInput{
		RoomID: rm.ID, SpeakerID: speaker, Title: "Standup " + uniqueSubject(t),
		FirstStartsAt: base, FirstEndsAt: base.Add(30 * time.Minute),
		Freq: "daily", IntervalCount: 1, Occurrences: 5,
	}

	series, results, err := repo.CreateSeries(ctx, creator, ev.ID, in)
	if err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	if series.Occurrences != 5 || series.Freq != "daily" {
		t.Fatalf("got series %+v", series)
	}
	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}

	for i, r := range results {
		if r.Status != "created" || r.SessionID == nil {
			t.Fatalf("occurrence %d: got %+v, want status=created with a SessionID", i, r)
		}
		wantStart := base.Add(time.Duration(i) * 24 * time.Hour)
		if !r.StartsAt.Equal(wantStart) {
			t.Errorf("occurrence %d: got StartsAt %v, want %v", i, r.StartsAt, wantStart)
		}

		got, err := repo.Get(ctx, ev.ID, *r.SessionID)
		if err != nil {
			t.Fatalf("occurrence %d: Get: %v", i, err)
		}
		if got.SeriesID == nil || *got.SeriesID != series.ID {
			t.Errorf("occurrence %d: got SeriesID %v, want %q -- every occurrence must be an ordinary sessions row tagged with the series", i, got.SeriesID, series.ID)
		}
		if !got.StartsAt.Equal(wantStart) {
			t.Errorf("occurrence %d: materialized StartsAt %v, want %v", i, got.StartsAt, wantStart)
		}
	}
}

// TestSessionRepository_CreateSeries_OccurrenceConflictIsPerItemNotWhole
// proves a conflict on one occurrence (item 16's EXCLUDE, the same room
// already booked at that exact instant by an unrelated session) doesn't
// abort the rest of the series -- a defined per-item result, matching item
// 21's BulkCreate precedent.
func TestSessionRepository_CreateSeries_OccurrenceConflictIsPerItemNotWhole(t *testing.T) {
	pool := testPool(t)
	sessionRepo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Series Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating room: %v", err)
	}

	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	// Pre-book the room for occurrence #2's exact slot (base + 1 day) with
	// a different speaker, so only the room EXCLUDE fires.
	blocker := createTestUser(t, pool)
	conflictAt := base.Add(24 * time.Hour)
	if _, err := sessionRepo.Create(ctx, creator, ev.ID, session.CreateInput{
		RoomID: rm.ID, SpeakerID: blocker, Title: "Blocker " + uniqueSubject(t),
		StartsAt: conflictAt, EndsAt: conflictAt.Add(30 * time.Minute),
	}); err != nil {
		t.Fatalf("seeding the blocking session: %v", err)
	}

	in := session.SeriesCreateInput{
		RoomID: rm.ID, SpeakerID: speaker, Title: "Standup " + uniqueSubject(t),
		FirstStartsAt: base, FirstEndsAt: base.Add(30 * time.Minute),
		Freq: "daily", IntervalCount: 1, Occurrences: 3,
	}
	_, results, err := sessionRepo.CreateSeries(ctx, creator, ev.ID, in)
	if err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Status != "created" {
		t.Errorf("occurrence 0: got status %q, want created", results[0].Status)
	}
	if results[1].Status != "conflict" {
		t.Errorf("occurrence 1: got status %q, want conflict", results[1].Status)
	}
	if results[2].Status != "created" {
		t.Errorf("occurrence 2: got status %q, want created -- one conflicting occurrence must not abort the rest of the series", results[2].Status)
	}
}

// TestSessionRepository_UpdateSeriesOccurrence_RecordsExceptionModified and
// its Delete-side sibling below prove item 23's other half: editing or
// cancelling one occurrence goes through the ordinary session PATCH/DELETE
// path (unchanged) and is additionally recorded in session_exceptions for
// history.
func TestSessionRepository_UpdateSeriesOccurrence_RecordsExceptionModified(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Series Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating room: %v", err)
	}
	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	_, results, err := repo.CreateSeries(ctx, creator, ev.ID, session.SeriesCreateInput{
		RoomID: rm.ID, SpeakerID: speaker, Title: "Standup " + uniqueSubject(t),
		FirstStartsAt: base, FirstEndsAt: base.Add(30 * time.Minute),
		Freq: "daily", IntervalCount: 1, Occurrences: 1,
	})
	if err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	occurrence, err := repo.Get(ctx, ev.ID, *results[0].SessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	updated, err := repo.Update(ctx, creator, ev.ID, occurrence.ID, occurrence.Version, session.Patch{Title: opt.Of("Renamed " + uniqueSubject(t))})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title == occurrence.Title {
		t.Fatal("title did not actually change -- test setup bug")
	}

	var status string
	var originalStartsAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, original_starts_at FROM session_exceptions WHERE session_id = $1`,
		occurrence.ID,
	).Scan(&status, &originalStartsAt); err != nil {
		t.Fatalf("querying session_exceptions: %v", err)
	}
	if status != "modified" {
		t.Errorf("got status %q, want modified", status)
	}
	if !originalStartsAt.Equal(occurrence.StartsAt) {
		t.Errorf("got original_starts_at %v, want %v (the occurrence's starts_at before this edit)", originalStartsAt, occurrence.StartsAt)
	}
}

func TestSessionRepository_DeleteSeriesOccurrence_RecordsExceptionCancelled(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Series Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating room: %v", err)
	}
	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	_, results, err := repo.CreateSeries(ctx, creator, ev.ID, session.SeriesCreateInput{
		RoomID: rm.ID, SpeakerID: speaker, Title: "Standup " + uniqueSubject(t),
		FirstStartsAt: base, FirstEndsAt: base.Add(30 * time.Minute),
		Freq: "daily", IntervalCount: 1, Occurrences: 1,
	})
	if err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	occurrence, err := repo.Get(ctx, ev.ID, *results[0].SessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, err := repo.Delete(ctx, creator, ev.ID, occurrence.ID, occurrence.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.Get(ctx, ev.ID, occurrence.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after delete: got err %v, want domain.ErrNotFound", err)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM session_exceptions WHERE session_id = $1`, occurrence.ID,
	).Scan(&status); err != nil {
		t.Fatalf("querying session_exceptions: %v", err)
	}
	if status != "cancelled" {
		t.Errorf("got status %q, want cancelled", status)
	}
}

// TestSessionRepository_StandaloneSession_NeverWritesASessionException
// proves the exception-writing code path is genuinely conditional on
// SeriesID -- an ordinary session's update must not spuriously create a
// session_exceptions row.
func TestSessionRepository_StandaloneSession_NeverWritesASessionException(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Standalone Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating room: %v", err)
	}
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Standalone "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Update(ctx, creator, ev.ID, created.ID, created.Version, session.Patch{Title: opt.Of("Renamed " + uniqueSubject(t))}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session_exceptions WHERE session_id = $1`, created.ID).Scan(&count); err != nil {
		t.Fatalf("counting session_exceptions: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d session_exceptions rows for a standalone session, want 0", count)
	}
}
