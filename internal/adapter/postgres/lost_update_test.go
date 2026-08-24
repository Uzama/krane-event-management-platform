package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// This file is item 17's must: test -- "a lost-update test: stale second
// write gets 409, first writer's data intact" -- proven across all four
// version-gated resources (events, rooms, sessions, event_members), not
// just one. Each test simulates the real two-writer shape the invariant
// protects against: both writers read the same version, the first writer's
// PATCH commits and bumps the version, and the second writer's PATCH -- sent
// with the version *it* read, now stale -- must be rejected with 409 and
// must not touch the row. The point isn't proving a bogus/never-issued
// version fails (existing StaleVersion tests already cover that); it's
// proving the winning writer's data is the data that survives, not
// reverted, not overwritten, not merged.

func TestEventRepository_LostUpdate_StaleSecondWriteRejected_FirstWritersDataIntact(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	created, err := repo.Create(ctx, creator, validCreateInput("Lost update "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	readVersion := created.Version // both writers "read" this version

	firstWriter, err := repo.Update(ctx, creator, created.ID, readVersion, event.Patch{Name: opt.Of("Winner")})
	if err != nil {
		t.Fatalf("first writer's Update: %v", err)
	}

	_, err = repo.Update(ctx, creator, created.ID, readVersion, event.Patch{Name: opt.Of("Loser")})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("second (stale) writer got err %v, want domain.ErrVersionMismatch", err)
	}

	current, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Name != "Winner" {
		t.Fatalf("got Name %q, want the first writer's %q intact", current.Name, "Winner")
	}
	if current.Version != firstWriter.Version {
		t.Fatalf("got Version %d, want the first writer's %d (second write must not have touched the row)", current.Version, firstWriter.Version)
	}
}

func TestRoomRepository_LostUpdate_StaleSecondWriteRejected_FirstWritersDataIntact(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Lost update "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	readVersion := created.Version

	firstWriter, err := repo.Update(ctx, creator, ev.ID, created.ID, readVersion, room.Patch{Name: opt.Of("Winner " + uniqueSubject(t))})
	if err != nil {
		t.Fatalf("first writer's Update: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev.ID, created.ID, readVersion, room.Patch{Name: opt.Of("Loser")})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("second (stale) writer got err %v, want domain.ErrVersionMismatch", err)
	}

	current, err := repo.Get(ctx, ev.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Name != firstWriter.Name {
		t.Fatalf("got Name %q, want the first writer's %q intact", current.Name, firstWriter.Name)
	}
	if current.Version != firstWriter.Version {
		t.Fatalf("got Version %d, want the first writer's %d (second write must not have touched the row)", current.Version, firstWriter.Version)
	}
}

func TestSessionRepository_LostUpdate_StaleSecondWriteRejected_FirstWritersDataIntact(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Lost update "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	readVersion := created.Version

	firstWriter, err := repo.Update(ctx, creator, ev.ID, created.ID, readVersion, session.Patch{Title: opt.Of("Winner")})
	if err != nil {
		t.Fatalf("first writer's Update: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev.ID, created.ID, readVersion, session.Patch{Title: opt.Of("Loser")})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("second (stale) writer got err %v, want domain.ErrVersionMismatch", err)
	}

	current, err := repo.Get(ctx, ev.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Title != "Winner" {
		t.Fatalf("got Title %q, want the first writer's %q intact", current.Title, "Winner")
	}
	if current.Version != firstWriter.Version {
		t.Fatalf("got Version %d, want the first writer's %d (second write must not have touched the row)", current.Version, firstWriter.Version)
	}
}

func TestMemberRepository_LostUpdate_StaleSecondAssignRoleRejected_FirstWritersDataIntact(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	ev := createTestEvent(t, pool, admin)

	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)
	created, err := repo.Create(ctx, admin, ev.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	readVersion := created.Version

	firstWriter, err := repo.AssignRole(ctx, admin, ev.ID, created.ID, readVersion, "contributor")
	if err != nil {
		t.Fatalf("first writer's AssignRole: %v", err)
	}

	_, err = repo.AssignRole(ctx, admin, ev.ID, created.ID, readVersion, "admin")
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("second (stale) writer got err %v, want domain.ErrVersionMismatch", err)
	}

	page, err := repo.List(ctx, ev.ID, 50, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var current *member.Member
	for i := range page.Members {
		if page.Members[i].ID == created.ID {
			current = &page.Members[i]
		}
	}
	if current == nil {
		t.Fatalf("member %q not found after the stale write attempt", created.ID)
	}
	if current.Role != "contributor" {
		t.Fatalf("got Role %q, want the first writer's %q intact", current.Role, "contributor")
	}
	if current.Version != firstWriter.Version {
		t.Fatalf("got Version %d, want the first writer's %d (second write must not have touched the row)", current.Version, firstWriter.Version)
	}
}
