package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
)

func fakeRequestHash(t *testing.T, body string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// TestInvitationRepository_BulkCreate_EachSuccessfulItemGetsItsOwnAuditRow
// is item 21's requirement (c): a bulk invite of N items that all succeed
// must produce N distinct audit_log rows -- one per invitation, each
// written inside that item's own Create call (the identical transaction
// item 13 already proved), never a single batch-level summary row. Also
// proves (a) indirectly: every item actually went through Create (which
// is the only code path that ever writes an invitation audit row).
func TestInvitationRepository_BulkCreate_EachSuccessfulItemGetsItsOwnAuditRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	items := []invitation.CreateInput{
		{Email: uniqueSubject(t) + "-1@example.com", Role: "attendee"},
		{Email: uniqueSubject(t) + "-2@example.com", Role: "attendee"},
		{Email: uniqueSubject(t) + "-3@example.com", Role: "attendee"},
	}

	result, err := repo.BulkCreate(ctx, admin, evt.ID, "key-"+uniqueSubject(t), fakeRequestHash(t, "body-1"), items)
	if err != nil {
		t.Fatalf("BulkCreate: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("got %d results, want 3", len(result.Items))
	}
	for i, r := range result.Items {
		if r.Status != "created" || r.InvitationID == nil {
			t.Fatalf("item %d: got %+v, want status=created with an InvitationID", i, r)
		}
	}

	var auditCount int
	ids := make([]string, len(result.Items))
	for i, r := range result.Items {
		ids[i] = *r.InvitationID
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE actor_id = $1 AND entity_type = 'invitation' AND entity_id = ANY($2)`,
		admin, ids,
	).Scan(&auditCount); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	if auditCount != 3 {
		t.Fatalf("got %d audit_log rows for 3 created invitations, want 3 (one per item, not one for the batch)", auditCount)
	}
}

// TestInvitationRepository_BulkCreate_PerItemEscalationGuardEnforced is
// item 21's requirement (a): the per-invite escalation guard (item 13,
// shared with member add) must run PER ITEM inside the batch, not hoisted
// once for the whole request. A contributor actor bulk-inviting one
// attendee (allowed) and one admin (not allowed) must get "created" for
// the first and "forbidden" for the second -- a batch-level guard would
// either wrongly allow both (hoisted-once-and-cached-permissive) or wrongly
// reject both (hoisted-once-and-cached-restrictive); only a genuine
// per-item check produces this exact split.
func TestInvitationRepository_BulkCreate_PerItemEscalationGuardEnforced(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	contributorEmail := uniqueSubject(t) + "@example.com"
	contributor := createTestUserWithEmail(t, pool, contributorEmail)
	if _, err := pool.Exec(ctx, `INSERT INTO event_members (event_id, user_id, role) VALUES ($1, $2, 'contributor')`, evt.ID, contributor); err != nil {
		t.Fatalf("seeding contributor membership: %v", err)
	}

	items := []invitation.CreateInput{
		{Email: uniqueSubject(t) + "-attendee@example.com", Role: "attendee"},
		{Email: uniqueSubject(t) + "-admin@example.com", Role: "admin"},
	}

	result, err := repo.BulkCreate(ctx, contributor, evt.ID, "key-"+uniqueSubject(t), fakeRequestHash(t, "body-2"), items)
	if err != nil {
		t.Fatalf("BulkCreate: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("got %d results, want 2", len(result.Items))
	}
	if result.Items[0].Status != "created" {
		t.Errorf("attendee-role item: got status %q, want created", result.Items[0].Status)
	}
	if result.Items[1].Status != "forbidden" {
		t.Errorf("admin-role item: got status %q, want forbidden -- a contributor must never be allowed to invite at admin, even inside a batch", result.Items[1].Status)
	}
}

// TestInvitationRepository_BulkCreate_PartialFailure_MixedResults proves a
// batch with some already-invited emails still completes, returning a
// defined per-item result for each -- never an all-or-nothing failure.
func TestInvitationRepository_BulkCreate_PartialFailure_MixedResults(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	alreadyInvited := uniqueSubject(t) + "-dup@example.com"
	if _, err := repo.Create(ctx, admin, evt.ID, invitation.CreateInput{Email: alreadyInvited, Role: "attendee"}); err != nil {
		t.Fatalf("seeding pre-existing invitation: %v", err)
	}

	items := []invitation.CreateInput{
		{Email: uniqueSubject(t) + "-new@example.com", Role: "attendee"},
		{Email: alreadyInvited, Role: "attendee"},
	}

	result, err := repo.BulkCreate(ctx, admin, evt.ID, "key-"+uniqueSubject(t), fakeRequestHash(t, "body-3"), items)
	if err != nil {
		t.Fatalf("BulkCreate: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("got %d results, want 2", len(result.Items))
	}
	if result.Items[0].Status != "created" {
		t.Errorf("new email: got status %q, want created", result.Items[0].Status)
	}
	if result.Items[1].Status != "conflict" {
		t.Errorf("already-invited email: got status %q, want conflict", result.Items[1].Status)
	}
}

// TestInvitationRepository_BulkCreate_RetrySameKeyAndHash_NoDuplicateSends
// is item 21's must: test -- a retry with the identical key and body
// replays the stored result and creates no new invitation rows.
func TestInvitationRepository_BulkCreate_RetrySameKeyAndHash_NoDuplicateSends(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	key := "key-" + uniqueSubject(t)
	hash := fakeRequestHash(t, "body-4")
	items := []invitation.CreateInput{{Email: uniqueSubject(t) + "@example.com", Role: "attendee"}}

	first, err := repo.BulkCreate(ctx, admin, evt.ID, key, hash, items)
	if err != nil {
		t.Fatalf("first BulkCreate: %v", err)
	}
	if first.Replayed {
		t.Fatal("first call reported Replayed=true, want false")
	}

	var countAfterFirst int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE event_id = $1`, evt.ID).Scan(&countAfterFirst); err != nil {
		t.Fatalf("counting invitations: %v", err)
	}

	second, err := repo.BulkCreate(ctx, admin, evt.ID, key, hash, items)
	if err != nil {
		t.Fatalf("second (retry) BulkCreate: %v", err)
	}
	if !second.Replayed {
		t.Fatal("second call reported Replayed=false, want true")
	}
	if len(second.Items) != len(first.Items) || second.Items[0].Email != first.Items[0].Email ||
		second.Items[0].Status != first.Items[0].Status ||
		(second.Items[0].InvitationID == nil) != (first.Items[0].InvitationID == nil) ||
		(first.Items[0].InvitationID != nil && *second.Items[0].InvitationID != *first.Items[0].InvitationID) {
		t.Fatalf("retry result differs from the original: first=%+v second=%+v", first.Items, second.Items)
	}

	var countAfterSecond int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE event_id = $1`, evt.ID).Scan(&countAfterSecond); err != nil {
		t.Fatalf("counting invitations: %v", err)
	}
	if countAfterSecond != countAfterFirst {
		t.Fatalf("invitation count changed on retry: %d -> %d, want unchanged (no double-send)", countAfterFirst, countAfterSecond)
	}
}

// TestInvitationRepository_BulkCreate_ReplayNeverReevaluatesAgainstCurrentState
// is item 21's requirement (b), proven directly rather than inferred: the
// first call succeeds while the actor is admin (allowed to invite at any
// role); the actor is then downgraded to contributor directly in the DB,
// a state under which re-running the SAME item would now be forbidden.
// The retry, with the identical key and hash, must still return the
// ORIGINAL "created" result -- proof it replayed the stored decision and
// never re-ran the escalation guard against the actor's current role.
func TestInvitationRepository_BulkCreate_ReplayNeverReevaluatesAgainstCurrentState(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	key := "key-" + uniqueSubject(t)
	hash := fakeRequestHash(t, "body-5")
	items := []invitation.CreateInput{{Email: uniqueSubject(t) + "@example.com", Role: "admin"}}

	first, err := repo.BulkCreate(ctx, admin, evt.ID, key, hash, items)
	if err != nil {
		t.Fatalf("first BulkCreate: %v", err)
	}
	if first.Items[0].Status != "created" {
		t.Fatalf("got status %q while actor is admin, want created", first.Items[0].Status)
	}

	if _, err := pool.Exec(ctx, `UPDATE event_members SET role = 'contributor' WHERE event_id = $1 AND user_id = $2`, evt.ID, admin); err != nil {
		t.Fatalf("downgrading actor to contributor: %v", err)
	}

	second, err := repo.BulkCreate(ctx, admin, evt.ID, key, hash, items)
	if err != nil {
		t.Fatalf("retry BulkCreate: %v", err)
	}
	if !second.Replayed {
		t.Fatal("retry reported Replayed=false, want true")
	}
	if second.Items[0].Status != "created" {
		t.Fatalf("got status %q on replay, want the ORIGINAL created -- re-evaluating against the actor's now-downgraded role would wrongly produce forbidden", second.Items[0].Status)
	}
}

// TestInvitationRepository_BulkCreate_SameKeyDifferentHash_ReturnsErrConflict
// is item 21's other must-case: reusing an Idempotency-Key for a
// genuinely different request body is rejected outright, not silently
// replayed and not silently reprocessed.
func TestInvitationRepository_BulkCreate_SameKeyDifferentHash_ReturnsErrConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewInvitationRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	key := "key-" + uniqueSubject(t)
	firstItems := []invitation.CreateInput{{Email: uniqueSubject(t) + "-a@example.com", Role: "attendee"}}
	if _, err := repo.BulkCreate(ctx, admin, evt.ID, key, fakeRequestHash(t, "body-a"), firstItems); err != nil {
		t.Fatalf("first BulkCreate: %v", err)
	}

	secondItems := []invitation.CreateInput{{Email: uniqueSubject(t) + "-b@example.com", Role: "attendee"}}
	_, err := repo.BulkCreate(ctx, admin, evt.ID, key, fakeRequestHash(t, "body-b"), secondItems)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want domain.ErrConflict (key reused with a different body)", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE event_id = $1 AND email = $2`, evt.ID, secondItems[0].Email).Scan(&count); err != nil {
		t.Fatalf("counting invitations: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d invitations for the rejected second request's email, want 0 -- it must not have been processed at all", count)
	}
}
