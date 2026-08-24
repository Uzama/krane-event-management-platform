package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
)

// createTestEvent inserts an event and grants creator an admin
// event_members row, via the real EventRepository -- member_repo tests need
// a live event with at least one admin to act as, the same way event_repo
// tests need a live user.
func createTestEvent(t *testing.T, pool *pgxpool.Pool, creator string) event.Event {
	t.Helper()
	got, err := postgres.NewEventRepository(pool).Create(context.Background(), creator, validCreateInput("Conf "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test event: %v", err)
	}
	return got
}

func createTestUserWithEmail(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	subject := uniqueSubject(t)
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (subject, email, name) VALUES ($1, $2, $3) RETURNING id::text`,
		subject, email, "Test User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("inserting test user: %v", err)
	}
	return id
}

func TestMemberRepository_Create_ResolvesEmailAndGrantsRole(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	target := createTestUserWithEmail(t, pool, targetEmail)

	got, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "contributor"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.UserID != target {
		t.Errorf("got UserID %q, want %q", got.UserID, target)
	}
	if got.Role != "contributor" {
		t.Errorf("got Role %q, want contributor", got.Role)
	}
	if got.Version != 1 {
		t.Errorf("got Version %d, want 1", got.Version)
	}

	// Reuses item 08's exact audit shape: entity_type='event_member',
	// entity_id=<membership row's own id>, action='create'.
	var entityType, entityID, action string
	var after []byte
	err = pool.QueryRow(ctx,
		`SELECT entity_type, entity_id::text, action, after FROM audit_log WHERE actor_id = $1 AND entity_type = 'event_member' ORDER BY created_at DESC LIMIT 1`,
		admin,
	).Scan(&entityType, &entityID, &action, &after)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if entityID != got.ID {
		t.Errorf("audit entity_id = %q, want the membership row's own id %q", entityID, got.ID)
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

func TestMemberRepository_Create_UnknownEmail_ReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	_, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: "nobody-" + uniqueSubject(t) + "@example.com", Role: "attendee"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

func TestMemberRepository_Create_DuplicateMembership_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)

	if _, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want ErrConflict", err)
	}
}

func TestMemberRepository_Create_ContributorCannotGrantElevatedRole(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	contributorEmail := uniqueSubject(t) + "@example.com"
	contributor := createTestUserWithEmail(t, pool, contributorEmail)
	if _, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: contributorEmail, Role: "contributor"}); err != nil {
		t.Fatalf("granting contributor: %v", err)
	}

	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)

	_, err := repo.Create(ctx, contributor, evt.ID, member.CreateInput{Email: targetEmail, Role: "admin"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("got err %v, want ErrForbidden", err)
	}

	if n := countRows(t, pool, `SELECT count(*) FROM event_members WHERE event_id = $1 AND role = 'admin'`, evt.ID); n != 1 {
		t.Errorf("admin count = %d, want 1 (the escalation must not have inserted anything)", n)
	}
}

func TestMemberRepository_Create_ContributorCanGrantAttendee(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	contributorEmail := uniqueSubject(t) + "@example.com"
	contributor := createTestUserWithEmail(t, pool, contributorEmail)
	if _, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: contributorEmail, Role: "contributor"}); err != nil {
		t.Fatalf("granting contributor: %v", err)
	}

	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)

	got, err := repo.Create(ctx, contributor, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Role != "attendee" {
		t.Errorf("got Role %q, want attendee", got.Role)
	}
}

func TestMemberRepository_List_KeysetPagination(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	var memberIDs []string
	for i := 0; i < 3; i++ {
		email := uniqueSubject(t) + "-" + time.Now().Format("150405.000000000") + "@example.com"
		createTestUserWithEmail(t, pool, email)
		m, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: email, Role: "attendee"})
		if err != nil {
			t.Fatalf("seeding member %d: %v", i, err)
		}
		memberIDs = append(memberIDs, m.ID)
	}

	// FAILURES.md: force ≥2 rows onto an identical created_at so the tie-break
	// on id is actually exercised, not accidentally passed by distinct
	// timestamps.
	tie := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := pool.Exec(ctx, `UPDATE event_members SET created_at = $1 WHERE id = ANY($2::uuid[])`, tie, memberIDs); err != nil {
		t.Fatalf("forcing tie: %v", err)
	}

	// admin's own auto-grant row plus 3 seeded = 4 total; page size 2.
	page1, err := repo.List(ctx, evt.ID, 2, nil)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Members) != 2 || page1.NextCursor == nil {
		t.Fatalf("page1 = %+v, want 2 members and a next cursor", page1)
	}

	page2, err := repo.List(ctx, evt.ID, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2.Members) != 2 {
		t.Fatalf("page2 = %+v, want 2 members", page2)
	}

	seen := map[string]bool{}
	for _, m := range append(page1.Members, page2.Members...) {
		if seen[m.ID] {
			t.Errorf("member %q returned twice across pages -- tie-break is broken", m.ID)
		}
		seen[m.ID] = true
	}
	if len(seen) != 4 {
		t.Errorf("saw %d distinct members across both pages, want 4", len(seen))
	}
}

func TestMemberRepository_AssignRole_Succeeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)
	m, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("seeding member: %v", err)
	}

	got, err := repo.AssignRole(ctx, admin, evt.ID, m.ID, m.Version, "contributor")
	if err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if got.Role != "contributor" {
		t.Errorf("got Role %q, want contributor", got.Role)
	}
	if got.Version != m.Version+1 {
		t.Errorf("got Version %d, want %d", got.Version, m.Version+1)
	}

	var entityType, action string
	var before, after []byte
	err = pool.QueryRow(ctx,
		`SELECT entity_type, action, before, after FROM audit_log WHERE entity_id = $1 AND action = 'update'`,
		m.ID,
	).Scan(&entityType, &action, &before, &after)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if entityType != "event_member" {
		t.Errorf("audit entity_type = %q, want event_member", entityType)
	}
	var beforeMap, afterMap map[string]any
	if err := json.Unmarshal(before, &beforeMap); err != nil {
		t.Fatalf("unmarshaling audit before: %v", err)
	}
	if err := json.Unmarshal(after, &afterMap); err != nil {
		t.Fatalf("unmarshaling audit after: %v", err)
	}
	if beforeMap["role"] != "attendee" || afterMap["role"] != "contributor" {
		t.Errorf("audit before/after roles = %v / %v, want attendee / contributor", beforeMap["role"], afterMap["role"])
	}
}

func TestMemberRepository_AssignRole_VersionMismatch_ReturnsVersionMismatch(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)
	m, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("seeding member: %v", err)
	}

	_, err = repo.AssignRole(ctx, admin, evt.ID, m.ID, m.Version+99, "contributor")
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want ErrVersionMismatch", err)
	}
}

func TestMemberRepository_AssignRole_MissingMember_ReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	_, err := repo.AssignRole(ctx, admin, evt.ID, "00000000-0000-0000-0000-000000000000", 1, "contributor")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

func TestMemberRepository_AssignRole_DemotingSoleAdmin_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	var adminMemberID string
	var version int
	err := pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, admin).
		Scan(&adminMemberID, &version)
	if err != nil {
		t.Fatalf("looking up admin membership: %v", err)
	}

	_, err = repo.AssignRole(ctx, admin, evt.ID, adminMemberID, version, "contributor")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want ErrConflict (last admin)", err)
	}

	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM event_members WHERE id = $1`, adminMemberID).Scan(&role); err != nil {
		t.Fatalf("re-querying membership: %v", err)
	}
	if role != "admin" {
		t.Errorf("got role %q after blocked demotion, want admin (unchanged)", role)
	}
}

func TestMemberRepository_AssignRole_DemotingAdminWithAnotherAdminPresent_Succeeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	secondAdminEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, secondAdminEmail)
	secondAdmin, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: secondAdminEmail, Role: "admin"})
	if err != nil {
		t.Fatalf("granting second admin: %v", err)
	}

	var firstAdminMemberID string
	var version int
	err = pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, admin).
		Scan(&firstAdminMemberID, &version)
	if err != nil {
		t.Fatalf("looking up first admin membership: %v", err)
	}

	got, err := repo.AssignRole(ctx, admin, evt.ID, firstAdminMemberID, version, "contributor")
	if err != nil {
		t.Fatalf("AssignRole with a second admin present: %v", err)
	}
	if got.Role != "contributor" {
		t.Errorf("got Role %q, want contributor", got.Role)
	}
	if secondAdmin.Role != "admin" {
		t.Errorf("sanity: second admin role = %q, want admin", secondAdmin.Role)
	}
}

func TestMemberRepository_Delete_Succeeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)
	m, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("seeding member: %v", err)
	}

	if err := repo.Delete(ctx, admin, evt.ID, m.ID, m.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM event_members WHERE id = $1`, m.ID); n != 0 {
		t.Errorf("event_members rows for id = %d, want 0 (hard delete)", n)
	}

	var entityType, action string
	var before, after []byte
	err = pool.QueryRow(ctx,
		`SELECT entity_type, action, before, after FROM audit_log WHERE entity_id = $1 AND action = 'delete'`,
		m.ID,
	).Scan(&entityType, &action, &before, &after)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if entityType != "event_member" {
		t.Errorf("audit entity_type = %q, want event_member", entityType)
	}
	if after != nil {
		t.Errorf("audit after = %s, want NULL on delete", after)
	}
	var beforeMap map[string]any
	if err := json.Unmarshal(before, &beforeMap); err != nil {
		t.Fatalf("unmarshaling audit before: %v", err)
	}
	if beforeMap["role"] != "attendee" {
		t.Errorf("audit before.role = %v, want attendee", beforeMap["role"])
	}
}

func TestMemberRepository_Delete_VersionMismatch_ReturnsVersionMismatch(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)
	targetEmail := uniqueSubject(t) + "@example.com"
	createTestUserWithEmail(t, pool, targetEmail)
	m, err := repo.Create(ctx, admin, evt.ID, member.CreateInput{Email: targetEmail, Role: "attendee"})
	if err != nil {
		t.Fatalf("seeding member: %v", err)
	}

	err = repo.Delete(ctx, admin, evt.ID, m.ID, m.Version+99)
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want ErrVersionMismatch", err)
	}
}

func TestMemberRepository_Delete_MissingMember_ReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	err := repo.Delete(ctx, admin, evt.ID, "00000000-0000-0000-0000-000000000000", 1)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

func TestMemberRepository_Delete_SoleAdmin_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewMemberRepository(pool)
	ctx := context.Background()

	admin := createTestUser(t, pool)
	evt := createTestEvent(t, pool, admin)

	var adminMemberID string
	var version int
	err := pool.QueryRow(ctx, `SELECT id::text, version FROM event_members WHERE event_id = $1 AND user_id = $2`, evt.ID, admin).
		Scan(&adminMemberID, &version)
	if err != nil {
		t.Fatalf("looking up admin membership: %v", err)
	}

	err = repo.Delete(ctx, admin, evt.ID, adminMemberID, version)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want ErrConflict (last admin)", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM event_members WHERE id = $1`, adminMemberID); n != 1 {
		t.Errorf("event_members rows for id = %d after blocked delete, want 1 (unchanged)", n)
	}
}
