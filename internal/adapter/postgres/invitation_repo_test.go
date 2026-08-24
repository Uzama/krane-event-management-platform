package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
)

func TestInvitationRepository_Create_AdminInvitesKnownEmail_ResolvesUserID(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	target := createTestUserWithEmail(t, pool, targetEmail)

	got, err := repo.Create(ctx, admin, evt.ID, invitation.CreateInput{Email: targetEmail, Role: "contributor"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.UserID == nil || *got.UserID != target {
		t.Errorf("got UserID %v, want %q", got.UserID, target)
	}
	if got.Email != targetEmail {
		t.Errorf("got Email %q, want %q", got.Email, targetEmail)
	}
	if got.Role != "contributor" {
		t.Errorf("got Role %q, want contributor", got.Role)
	}

	var entityType, entityID, action string
	var after []byte
	err = pool.QueryRow(ctx,
		`SELECT entity_type, entity_id::text, action, after FROM audit_log WHERE actor_id = $1 AND entity_type = 'invitation' ORDER BY created_at DESC LIMIT 1`,
		admin,
	).Scan(&entityType, &entityID, &action, &after)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if entityID != got.ID {
		t.Errorf("audit entity_id = %q, want %q", entityID, got.ID)
	}
	if action != "create" {
		t.Errorf("audit action = %q, want create", action)
	}
	var afterMap map[string]any
	if err := json.Unmarshal(after, &afterMap); err != nil {
		t.Fatalf("unmarshaling audit after: %v", err)
	}
	if afterMap["role"] != "contributor" {
		t.Errorf("audit after.role = %v, want contributor", afterMap["role"])
	}
}

// TestInvitationRepository_Create_UnknownEmail_LeavesUserIDNilNotError proves
// D2: inviting someone who has never signed in is not an error -- unlike
// member add, which requires the email resolve to an existing user.
func TestInvitationRepository_Create_UnknownEmail_LeavesUserIDNilNotError(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	got, err := repo.Create(ctx, admin, evt.ID, invitation.CreateInput{Email: "nobody-" + uniqueSubject(t) + "@example.com", Role: "attendee"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.UserID != nil {
		t.Errorf("got UserID %v, want nil (email never signed in)", *got.UserID)
	}
}

func TestInvitationRepository_Create_DuplicateEmail_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"

	if _, err := repo.Create(ctx, admin, evt.ID, invitation.CreateInput{Email: targetEmail, Role: "attendee"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := repo.Create(ctx, admin, evt.ID, invitation.CreateInput{Email: targetEmail, Role: "attendee"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want ErrConflict", err)
	}
}

func TestInvitationRepository_Create_ContributorCannotInviteAboveAttendee(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	contributorEmail := uniqueSubject(t) + "@example.com"
	contributor := createTestUserWithEmail(t, pool, contributorEmail)
	memberRepo := postgres.NewMemberRepository(pool)
	if _, err := memberRepo.Create(ctx, admin, evt.ID, member.CreateInput{Email: contributorEmail, Role: "contributor"}); err != nil {
		t.Fatalf("granting contributor: %v", err)
	}

	targetEmail := uniqueSubject(t) + "@example.com"

	_, err := repo.Create(ctx, contributor, evt.ID, invitation.CreateInput{Email: targetEmail, Role: "admin"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("got err %v, want ErrForbidden", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM invitations WHERE event_id = $1 AND email = $2`, evt.ID, targetEmail); n != 0 {
		t.Errorf("invitations rows = %d, want 0 (the escalation must not have inserted anything)", n)
	}
}

func TestInvitationRepository_Create_ContributorCanInviteAttendee(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	contributorEmail := uniqueSubject(t) + "@example.com"
	contributor := createTestUserWithEmail(t, pool, contributorEmail)
	memberRepo := postgres.NewMemberRepository(pool)
	if _, err := memberRepo.Create(ctx, admin, evt.ID, member.CreateInput{Email: contributorEmail, Role: "contributor"}); err != nil {
		t.Fatalf("granting contributor: %v", err)
	}

	targetEmail := uniqueSubject(t) + "@example.com"
	got, err := repo.Create(ctx, contributor, evt.ID, invitation.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Role != "attendee" {
		t.Errorf("got Role %q, want attendee", got.Role)
	}
}

// TestInvitationRepository_Create_NonMemberActor_CannotInviteEvenAttendee
// proves roles.go's canGrantRoleGuard fails CLOSED for invitations too: an
// actor with no event_members row on this event must be denied every
// invite, including role=attendee. This repo call bypasses the
// http/middleware Authz chokepoint on purpose, the same as the analogous
// member_repo_test.go case -- the guard proven here is the second line of
// defense, not the only one.
func TestInvitationRepository_Create_NonMemberActor_CannotInviteEvenAttendee(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	nonMember := createTestUser(t, pool)

	targetEmail := uniqueSubject(t) + "@example.com"
	_, err := repo.Create(ctx, nonMember, evt.ID, invitation.CreateInput{Email: targetEmail, Role: "attendee"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("got err %v, want ErrForbidden (non-member actor, even for role=attendee)", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM invitations WHERE event_id = $1 AND email = $2`, evt.ID, targetEmail); n != 0 {
		t.Errorf("invitations rows = %d, want 0 (the fail-open invite must not have inserted anything)", n)
	}
}

func TestInvitationRepository_List_KeysetPagination(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	var invitationIDs []string
	for i := 0; i < 4; i++ {
		email := uniqueSubject(t) + "-" + time.Now().Format("150405.000000000") + "@example.com"
		inv, err := repo.Create(ctx, admin, evt.ID, invitation.CreateInput{Email: email, Role: "attendee"})
		if err != nil {
			t.Fatalf("seeding invitation %d: %v", i, err)
		}
		invitationIDs = append(invitationIDs, inv.ID)
	}

	// FAILURES.md: force ≥2 rows onto an identical created_at so the tie-break
	// on id is actually exercised, not accidentally passed by distinct
	// timestamps.
	tie := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := pool.Exec(ctx, `UPDATE invitations SET created_at = $1 WHERE id = ANY($2::uuid[])`, tie, invitationIDs); err != nil {
		t.Fatalf("forcing tie: %v", err)
	}

	page1, err := repo.List(ctx, evt.ID, 2, nil)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Invitations) != 2 || page1.NextCursor == nil {
		t.Fatalf("page1 = %+v, want 2 invitations and a next cursor", page1)
	}

	page2, err := repo.List(ctx, evt.ID, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2.Invitations) != 2 {
		t.Fatalf("page2 = %+v, want 2 invitations", page2)
	}

	seen := map[string]bool{}
	for _, inv := range append(page1.Invitations, page2.Invitations...) {
		if seen[inv.ID] {
			t.Errorf("invitation %q returned twice across pages -- tie-break is broken", inv.ID)
		}
		seen[inv.ID] = true
	}
	if len(seen) != 4 {
		t.Errorf("saw %d distinct invitations across both pages, want 4", len(seen))
	}
}
