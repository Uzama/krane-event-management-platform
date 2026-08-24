package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// TestSessionRepository_Create_ConcurrentOverlappingRoomBookings_ExactlyOneSucceeds
// is item 16's required race test: two goroutines released together on a
// barrier POST overlapping sessions in the same room. Unlike item 09's
// admin-count race (a check-then-act application-level race that needed a
// deterministic test-only synchronization seam to force overlap
// reliably -- see ISSUE.md's 2026-08-24 entry), this constraint is enforced
// by Postgres's own EXCLUDE, evaluated atomically as part of each INSERT's
// index insertion -- there is no read-then-write window in application code
// to miss, so a bare goroutine barrier is sufficient to prove the test
// actually races: both transactions attempt to insert their conflicting
// index entry, and Postgres's own index-level locking serializes them,
// rejecting whichever commits second.
func TestSessionRepository_Create_ConcurrentOverlappingRoomBookings_ExactlyOneSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speakerA := createTestUser(t, pool)
	speakerB := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}

	starts := time.Now().UTC().Truncate(time.Millisecond)
	inputs := []session.CreateInput{
		{RoomID: rm.ID, SpeakerID: speakerA, Title: "Talk A", StartsAt: starts, EndsAt: starts.Add(time.Hour)},
		// Overlaps the first by 30 minutes -- same room, different speaker,
		// so only the room EXCLUDE (not the speaker EXCLUDE) can catch it.
		{RoomID: rm.ID, SpeakerID: speakerB, Title: "Talk B", StartsAt: starts.Add(30 * time.Minute), EndsAt: starts.Add(90 * time.Minute)},
	}

	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)

	results := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range inputs {
		go func(i int) {
			defer wg.Done()
			ready.Done()
			start.Wait()
			_, err := repo.Create(ctx, creator, ev.ID, inputs[i])
			results[i] = err
		}(i)
	}
	ready.Wait()
	start.Done()
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if successes != 1 || conflicts != 1 {
		t.Fatalf("got %d successes and %d conflicts, want exactly 1 and 1 (results: %v)", successes, conflicts, results)
	}
}
