package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
)

// TestRoomRepository_Update_ConcurrentStaleWriters_ExactlyOneSucceeds is the
// concurrent form of lost_update_test.go's room case: two writers who both
// read version 1 are released together on a barrier and both PATCH with
// version 1. Exactly one must win and the other must get ErrVersionMismatch,
// and the row must carry the winner's data at version 2 -- never
// last-write-wins, never a merge, never both.
//
// A bare goroutine barrier is sufficient here for the same reason it is for
// item 16's EXCLUDE race, and unlike item 09's admin-count race: the whole
// check is the single atomic statement
//
//	UPDATE rooms SET ..., version = version + 1 WHERE id = $1 AND version = $2
//
// There is no read-then-write window in Go for scheduling jitter to hide.
// Postgres serialises the two UPDATEs on the row lock and, under READ
// COMMITTED, the second re-evaluates its WHERE against the committed row
// (version is now 2, so `version = 1` matches nothing). The sequential
// lost-update test already proves the same code path; this one proves it
// holds when the two writes arrive at the same instant, which is the
// question the brief actually asks. Falsified during feature 29 by removing
// `"version": version` from the UPDATE's WHERE: both writers succeeded and
// the row ended at version 3.
func TestRoomRepository_Update_ConcurrentStaleWriters_ExactlyOneSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Race "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	readVersion := created.Version // both writers "read" this version

	names := []string{"Writer A " + uniqueSubject(t), "Writer B " + uniqueSubject(t)}

	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)

	results := make([]room.Room, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range names {
		go func(i int) {
			defer wg.Done()
			ready.Done()
			start.Wait()
			results[i], errs[i] = repo.Update(ctx, creator, ev.ID, created.ID, readVersion, room.Patch{Name: opt.Of(names[i])})
		}(i)
	}
	ready.Wait()
	start.Done()
	wg.Wait()

	winner := -1
	successes, mismatches := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			successes++
			winner = i
		case errors.Is(err, domain.ErrVersionMismatch):
			mismatches++
		default:
			t.Fatalf("writer %d: unexpected error: %v", i, err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Fatalf("got %d successes and %d version mismatches, want exactly 1 and 1 (errs: %v)", successes, mismatches, errs)
	}

	// The row is the winner's, at exactly one bump -- the loser touched nothing.
	current, err := repo.Get(ctx, ev.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Name != names[winner] {
		t.Fatalf("got Name %q, want the winning writer's %q intact", current.Name, names[winner])
	}
	if current.Version != readVersion+1 || current.Version != results[winner].Version {
		t.Fatalf("got Version %d, want %d (one write applied, not two)", current.Version, readVersion+1)
	}
}
