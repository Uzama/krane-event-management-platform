package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
)

// TestMemberRepository_Delete_ConcurrentSelfRemovalOfBothAdmins_ExactlyOneSucceeds
// is item 09's required race test, same shape as item 16's future
// double-booking race: two goroutines released together on a barrier, each
// deleting one of an event's exactly-two admins concurrently. Without the
// SELECT ... FOR UPDATE lock in MemberRepository.Delete, both transactions
// could each see the other admin still present (neither has committed yet)
// and both succeed, leaving zero admins. The lock serializes them: the
// second transaction blocks until the first commits, then re-checks a
// fresh count and is correctly rejected.
func TestMemberRepository_Delete_ConcurrentSelfRemovalOfBothAdmins_ExactlyOneSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	firstAdmin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, firstAdmin)

	secondAdminEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, secondAdminEmail)
	secondAdminMember, err := repo.Create(ctx, firstAdmin, evt.ID, member.CreateInput{Email: secondAdminEmail, Role: "admin"})
	if err != nil {
		t.Fatalf("granting second admin: %v", err)
	}

	var firstAdminMemberID string
	var firstVersion int
	err = pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, firstAdmin).
		Scan(&firstAdminMemberID, &firstVersion)
	if err != nil {
		t.Fatalf("looking up first admin membership: %v", err)
	}

	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)

	results := make([]error, 2)
	targets := []struct {
		memberID string
		version  int
	}{
		{firstAdminMemberID, firstVersion},
		{secondAdminMember.ID, secondAdminMember.Version},
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			start.Wait() // barrier: both goroutines fire together, not sequentially
			results[i] = repo.Delete(ctx, firstAdmin, evt.ID, targets[i].memberID, targets[i].version)
		}()
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
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("got %d successful deletes, want exactly 1", successes)
	}
	if conflicts != 1 {
		t.Errorf("got %d ErrConflict results, want exactly 1 (the last-admin guard should have blocked the second)", conflicts)
	}

	var remainingAdmins int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM event_members WHERE event_id = $1 AND role = 'admin'`, evt.ID).Scan(&remainingAdmins)
	if err != nil {
		t.Fatalf("counting remaining admins: %v", err)
	}
	if remainingAdmins != 1 {
		t.Fatalf("event %s ended with %d admins, want exactly 1 (orphaned or over-protected)", evt.ID, remainingAdmins)
	}
}

// TestMemberRepository_Delete_EventLockSerializesAdminCountAffectingWrites
// proves the mutual-exclusion property directly, rather than only inferring
// it from the outcome above. A bare barrier around two fast DELETE calls
// almost never lands both goroutines inside the real vulnerable window (see
// AfterEventLockForTest's doc comment -- confirmed empirically during
// development: the outcome-based test above passed 20/20 with the lock
// removed and no seam forcing overlap). This test uses the seam to force
// genuine overlap and assert directly that at most one goroutine is ever
// "inside" the post-lock section at a time -- if the events-row FOR UPDATE
// lock were ever removed, this test fails deterministically, every run, not
// only when timing happens to cooperate.
func TestMemberRepository_Delete_EventLockSerializesAdminCountAffectingWrites(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	firstAdmin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, firstAdmin)

	secondAdminEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, secondAdminEmail)
	secondAdminMember, err := repo.Create(ctx, firstAdmin, evt.ID, member.CreateInput{Email: secondAdminEmail, Role: "admin"})
	if err != nil {
		t.Fatalf("granting second admin: %v", err)
	}

	var firstAdminMemberID string
	var firstVersion int
	err = pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, firstAdmin).
		Scan(&firstAdminMemberID, &firstVersion)
	if err != nil {
		t.Fatalf("looking up first admin membership: %v", err)
	}

	var active int32
	var overlapped atomic.Bool
	postgres.AfterEventLockForTest = func() {
		if atomic.AddInt32(&active, 1) > 1 {
			overlapped.Store(true)
		}
		time.Sleep(150 * time.Millisecond) // widen the window -- test-only, never in production
		atomic.AddInt32(&active, -1)
	}
	t.Cleanup(func() { postgres.AfterEventLockForTest = func() {} })

	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)

	results := make([]error, 2)
	targets := []struct {
		memberID string
		version  int
	}{
		{firstAdminMemberID, firstVersion},
		{secondAdminMember.ID, secondAdminMember.Version},
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			start.Wait()
			results[i] = repo.Delete(ctx, firstAdmin, evt.ID, targets[i].memberID, targets[i].version)
		}()
	}

	ready.Wait()
	start.Done()
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("both goroutines were inside the post-events-lock section concurrently -- SELECT ... FOR UPDATE did not serialize them")
	}

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("got %d successes, %d conflicts, want exactly 1 of each", successes, conflicts)
	}
}

// TestMemberRepository_AssignRole_ConcurrentSelfDemotionOfBothAdmins_ExactlyOneSucceeds
// is AssignRole's counterpart to the Delete race test above -- a separate
// code path with its own SELECT ... FOR UPDATE call site, so it needs its
// own proof, not an inference from Delete's. Two goroutines released on a
// barrier each demote one of an event's exactly-two admins to contributor
// concurrently.
func TestMemberRepository_AssignRole_ConcurrentSelfDemotionOfBothAdmins_ExactlyOneSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	firstAdmin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, firstAdmin)

	secondAdminEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, secondAdminEmail)
	secondAdminMember, err := repo.Create(ctx, firstAdmin, evt.ID, member.CreateInput{Email: secondAdminEmail, Role: "admin"})
	if err != nil {
		t.Fatalf("granting second admin: %v", err)
	}

	var firstAdminMemberID string
	var firstVersion int
	err = pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, firstAdmin).
		Scan(&firstAdminMemberID, &firstVersion)
	if err != nil {
		t.Fatalf("looking up first admin membership: %v", err)
	}

	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)

	results := make([]error, 2)
	targets := []struct {
		memberID string
		version  int
	}{
		{firstAdminMemberID, firstVersion},
		{secondAdminMember.ID, secondAdminMember.Version},
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			start.Wait()
			_, results[i] = repo.AssignRole(ctx, firstAdmin, evt.ID, targets[i].memberID, targets[i].version, "contributor")
		}()
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
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("got %d successful demotions, want exactly 1", successes)
	}
	if conflicts != 1 {
		t.Errorf("got %d ErrConflict results, want exactly 1 (the last-admin guard should have blocked the second)", conflicts)
	}

	var remainingAdmins int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM event_members WHERE event_id = $1 AND role = 'admin'`, evt.ID).Scan(&remainingAdmins)
	if err != nil {
		t.Fatalf("counting remaining admins: %v", err)
	}
	if remainingAdmins != 1 {
		t.Fatalf("event %s ended with %d admins, want exactly 1 (orphaned or over-protected)", evt.ID, remainingAdmins)
	}
}

// TestMemberRepository_AssignRole_EventLockSerializesAdminCountAffectingWrites
// is the deterministic counterpart for AssignRole, same shape as Delete's --
// see that test's doc comment for why a bare barrier cannot be trusted on
// its own.
func TestMemberRepository_AssignRole_EventLockSerializesAdminCountAffectingWrites(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	firstAdmin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, firstAdmin)

	secondAdminEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, secondAdminEmail)
	secondAdminMember, err := repo.Create(ctx, firstAdmin, evt.ID, member.CreateInput{Email: secondAdminEmail, Role: "admin"})
	if err != nil {
		t.Fatalf("granting second admin: %v", err)
	}

	var firstAdminMemberID string
	var firstVersion int
	err = pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, firstAdmin).
		Scan(&firstAdminMemberID, &firstVersion)
	if err != nil {
		t.Fatalf("looking up first admin membership: %v", err)
	}

	var active int32
	var overlapped atomic.Bool
	postgres.AfterEventLockForTest = func() {
		if atomic.AddInt32(&active, 1) > 1 {
			overlapped.Store(true)
		}
		time.Sleep(150 * time.Millisecond) // widen the window -- test-only, never in production
		atomic.AddInt32(&active, -1)
	}
	t.Cleanup(func() { postgres.AfterEventLockForTest = func() {} })

	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)

	results := make([]error, 2)
	targets := []struct {
		memberID string
		version  int
	}{
		{firstAdminMemberID, firstVersion},
		{secondAdminMember.ID, secondAdminMember.Version},
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			start.Wait()
			_, results[i] = repo.AssignRole(ctx, firstAdmin, evt.ID, targets[i].memberID, targets[i].version, "contributor")
		}()
	}

	ready.Wait()
	start.Done()
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("both goroutines were inside the post-events-lock section concurrently -- SELECT ... FOR UPDATE did not serialize them")
	}

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("got %d successes, %d conflicts, want exactly 1 of each", successes, conflicts)
	}
}
