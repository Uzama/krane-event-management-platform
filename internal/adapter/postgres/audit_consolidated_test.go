package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// This file is item 22's consolidated proof. Every mutation already writes
// its audit_log row correctly, per entity type, in its own tests
// (event_repo_test.go, room_repo_test.go, session_repo_test.go,
// member_repo_test.go, invitation_repo_test.go) -- item 22 does not
// introduce new audit-writing capability. It exists to prove the
// cross-cutting invariant (actor/before/after captured, log append-only)
// in ONE place, across all five entity types uniformly, rather than
// leaving that as an inference from five separately-scoped test files.

// auditRow reads back the most recent audit_log row for (actorID,
// entityType, entityID) -- the shape every one of the five checks below
// needs.
func auditRow(t *testing.T, pool *pgxpool.Pool, actorID, entityType, entityID string) (action string, before, after map[string]any) {
	t.Helper()
	var beforeRaw, afterRaw []byte
	err := pool.QueryRow(context.Background(),
		`SELECT action, before, after FROM audit_log
		 WHERE actor_id = $1 AND entity_type = $2 AND entity_id = $3
		 ORDER BY created_at DESC LIMIT 1`,
		actorID, entityType, entityID,
	).Scan(&action, &beforeRaw, &afterRaw)
	if err != nil {
		t.Fatalf("querying audit_log for %s %s: %v", entityType, entityID, err)
	}
	if beforeRaw != nil {
		if err := json.Unmarshal(beforeRaw, &before); err != nil {
			t.Fatalf("unmarshaling before: %v", err)
		}
	}
	if afterRaw != nil {
		if err := json.Unmarshal(afterRaw, &after); err != nil {
			t.Fatalf("unmarshaling after: %v", err)
		}
	}
	return action, before, after
}

// TestAuditLog_EveryCreateAcrossAllFiveEntityTypesRecordsActorAndAfter is
// item 22's must: (the actor/before/after half), proven uniformly across
// event/event_member/room/session/invitation in one place. A create's
// before is always NULL by design (there is no prior state) -- the
// update case below is what proves before is captured too.
func TestAuditLog_EveryCreateAcrossAllFiveEntityTypesRecordsActorAndAfter(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool)

	t.Run("event", func(t *testing.T) {
		ev, err := postgres.NewEventRepository(pool).Create(ctx, creator, validCreateInput("Audit "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "event", ev.ID)
		if action != "create" || before != nil || after["name"] != ev.Name {
			t.Fatalf("got action=%q before=%v after.name=%v, want create/nil/%q", action, before, after["name"], ev.Name)
		}
	})

	t.Run("event_member", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		targetEmail := uniqueSubject(t) + "@example.com"
		createTestUserWithEmail(t, pool, targetEmail)
		m, err := postgres.NewMemberRepository(pool).Create(ctx, creator, ev.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "event_member", m.ID)
		if action != "create" || before != nil || after["role"] != "attendee" {
			t.Fatalf("got action=%q before=%v after.role=%v, want create/nil/attendee", action, before, after["role"])
		}
	})

	t.Run("room", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Audit Hall "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "room", rm.ID)
		if action != "create" || before != nil || after["name"] != rm.Name {
			t.Fatalf("got action=%q before=%v after.name=%v, want create/nil/%q", action, before, after["name"], rm.Name)
		}
	})

	t.Run("session", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		speaker := createTestUser(t, pool)
		rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Audit Hall "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("creating room: %v", err)
		}
		s, err := postgres.NewSessionRepository(pool).Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Audit Session "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "session", s.ID)
		if action != "create" || before != nil || after["title"] != s.Title {
			t.Fatalf("got action=%q before=%v after.title=%v, want create/nil/%q", action, before, after["title"], s.Title)
		}
	})

	t.Run("invitation", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		email := uniqueSubject(t) + "@example.com"
		inv, err := postgres.NewInvitationRepository(pool).Create(ctx, creator, ev.ID, invitation.CreateInput{Email: email, Role: "attendee"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "invitation", inv.ID)
		if action != "create" || before != nil || after["email"] != email {
			t.Fatalf("got action=%q before=%v after.email=%v, want create/nil/%q", action, before, after["email"], email)
		}
	})
}

// TestAuditLog_UpdateAcrossAllFourVersionedEntityTypesRecordsBeforeAndAfter
// is the before-is-captured half -- event_members' AssignRole is included
// alongside the three PATCH-able resources since it's also a versioned
// update with a real before/after transition. Invitations have no update
// path (item 13, D-scoped) so they're not part of this half.
func TestAuditLog_UpdateAcrossAllFourVersionedEntityTypesRecordsBeforeAndAfter(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool)

	t.Run("event", func(t *testing.T) {
		ev, err := postgres.NewEventRepository(pool).Create(ctx, creator, validCreateInput("Before "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := postgres.NewEventRepository(pool).Update(ctx, creator, ev.ID, ev.Version, event.Patch{Name: opt.Of("After " + uniqueSubject(t))}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "event", ev.ID)
		if action != "update" || before["name"] == after["name"] {
			t.Fatalf("got action=%q before.name=%v after.name=%v, want update with before != after", action, before["name"], after["name"])
		}
	})

	t.Run("event_member", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		targetEmail := uniqueSubject(t) + "@example.com"
		createTestUserWithEmail(t, pool, targetEmail)
		m, err := postgres.NewMemberRepository(pool).Create(ctx, creator, ev.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := postgres.NewMemberRepository(pool).AssignRole(ctx, creator, ev.ID, m.ID, m.Version, "contributor"); err != nil {
			t.Fatalf("AssignRole: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "event_member", m.ID)
		if action != "update" || before["role"] != "attendee" || after["role"] != "contributor" {
			t.Fatalf("got action=%q before.role=%v after.role=%v, want update/attendee/contributor", action, before["role"], after["role"])
		}
	})

	t.Run("room", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Before Hall "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := postgres.NewRoomRepository(pool).Update(ctx, creator, ev.ID, rm.ID, rm.Version, room.Patch{Name: opt.Of("After Hall " + uniqueSubject(t))}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "room", rm.ID)
		if action != "update" || before["name"] == after["name"] {
			t.Fatalf("got action=%q before.name=%v after.name=%v, want update with before != after", action, before["name"], after["name"])
		}
	})

	t.Run("session", func(t *testing.T) {
		ev := createTestEvent(t, pool, creator)
		speaker := createTestUser(t, pool)
		rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Before Hall "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("creating room: %v", err)
		}
		s, err := postgres.NewSessionRepository(pool).Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Before Title "+uniqueSubject(t)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := postgres.NewSessionRepository(pool).Update(ctx, creator, ev.ID, s.ID, s.Version, session.Patch{Title: opt.Of("After Title " + uniqueSubject(t))}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		action, before, after := auditRow(t, pool, creator, "session", s.ID)
		if action != "update" || before["title"] == after["title"] {
			t.Fatalf("got action=%q before.title=%v after.title=%v, want update with before != after", action, before["title"], after["title"])
		}
	})
}

// TestAuditLog_CannotBeMutatedByRuntimeRole is item 22's other must: half
// -- "the log can't be mutated" -- proven behaviorally (a live UPDATE/
// DELETE attempt as the actual runtime role, not merely a
// has_table_privilege metadata check, which migrations/schema_test.go's
// TestPrivilegeMatrixApplied already covers at the schema level for item
// 03). testPool connects as krane_app -- the same role that ends up
// running here, credentials taken from TEST_DATABASE_URL/its default,
// confirmed in user_repo_test.go's own doc comment.
func TestAuditLog_CannotBeMutatedByRuntimeRole(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev, err := postgres.NewEventRepository(pool).Create(ctx, creator, validCreateInput("Immutable "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var auditID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM audit_log WHERE actor_id = $1 AND entity_type = 'event' AND entity_id = $2`,
		creator, ev.ID,
	).Scan(&auditID); err != nil {
		t.Fatalf("finding the audit row: %v", err)
	}

	t.Run("UPDATE", func(t *testing.T) {
		_, err := pool.Exec(ctx, `UPDATE audit_log SET action = 'tampered' WHERE id = $1`, auditID)
		assertPermissionDenied(t, err)
	})
	t.Run("DELETE", func(t *testing.T) {
		_, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, auditID)
		assertPermissionDenied(t, err)
	})

	var stillAction string
	if err := pool.QueryRow(ctx, `SELECT action FROM audit_log WHERE id = $1`, auditID).Scan(&stillAction); err != nil {
		t.Fatalf("re-reading the audit row after the denied attempts: %v", err)
	}
	if stillAction != "create" {
		t.Fatalf("audit row's action = %q, want unchanged create -- a denied UPDATE must not have partially applied", stillAction)
	}
}

func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("got no error, want a permission-denied failure (audit_log must be append-only)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("got err %v, want SQLSTATE 42501 (insufficient_privilege)", err)
	}
}
