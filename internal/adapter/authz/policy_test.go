package authz_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/authz"
	domainauthz "github.com/Uzama/krane-event-management-platform/internal/domain/authz"
)

// randomUUIDv4 generates a syntactically valid, essentially-never-colliding
// uuid string for tests that need an event id guaranteed not to exist --
// avoids pulling in a uuid dependency for one test helper (CLAUDE.md:
// prefer the standard library).
func randomUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Matches the Makefile's TEST_DATABASE_URL default (see migrations/schema_test.go,
// internal/container/container_test.go, internal/adapter/postgres/user_repo_test.go).
const defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultTestDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v\n\nThe suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func newPolicy(t *testing.T, pool *pgxpool.Pool) *authz.Policy {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := authz.New(ctx, pool)
	if err != nil {
		t.Fatalf("authz.New: %v\n\nThe suite needs migrations/20260824090000_seed_role_permissions.up.sql applied -- run `make up`.", err)
	}
	return p
}

// seedUser inserts a users row with a unique subject so parallel packages
// never collide (CLAUDE.md: no test may assume it's the only occupant of
// the database).
func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	subject := fmt.Sprintf("authz-test-%s-%d", t.Name(), time.Now().UnixNano())

	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (subject, email, name) VALUES ($1, $2, $3) RETURNING id::text`,
		subject, subject+"@test.krane", "Authz Test User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return id
}

func seedEvent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (name, timezone, starts_at, ends_at)
		 VALUES ($1, 'UTC', now(), now() + interval '1 day')
		 RETURNING id::text`,
		"Authz Test Event "+t.Name(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding event: %v", err)
	}
	return id
}

func seedMember(t *testing.T, pool *pgxpool.Pool, eventID, userID, role string) {
	t.Helper()

	_, err := pool.Exec(context.Background(),
		`INSERT INTO event_members (event_id, user_id, role) VALUES ($1, $2, $3)`,
		eventID, userID, role,
	)
	if err != nil {
		t.Fatalf("seeding event_members: %v", err)
	}
}

// expectedMatrix is transcribed directly from docs/requirements.md §4's
// table -- NOT generated from, or read out of,
// migrations/20260824090000_seed_role_permissions.up.sql. Comparing
// against an independently-transcribed copy of the design doc (not just
// checking Policy is internally self-consistent with whatever the
// migration happened to load) is what lets this test catch a migration bug:
// a dropped, duplicated, or mis-typed row. The residual risk this doesn't
// cover: both this table and the migration were transcribed from the same
// source document by the same author in the same session, so a shared
// misreading of docs/requirements.md §4 itself would pass both. Guarding
// against that would need a second, independent reader of the design doc.
var expectedMatrix = map[string][]struct {
	Resource domainauthz.Resource
	Action   domainauthz.Action
}{
	"admin": {
		{domainauthz.ResourceEvent, domainauthz.ActionRead},
		{domainauthz.ResourceEvent, domainauthz.ActionUpdate},
		{domainauthz.ResourceEvent, domainauthz.ActionDelete},
		{domainauthz.ResourceMember, domainauthz.ActionRead},
		{domainauthz.ResourceMember, domainauthz.ActionCreate},
		{domainauthz.ResourceMember, domainauthz.ActionDelete},
		{domainauthz.ResourceMember, domainauthz.ActionAssignRole},
		{domainauthz.ResourceRoom, domainauthz.ActionCreate},
		{domainauthz.ResourceRoom, domainauthz.ActionRead},
		{domainauthz.ResourceRoom, domainauthz.ActionUpdate},
		{domainauthz.ResourceRoom, domainauthz.ActionDelete},
		{domainauthz.ResourceSession, domainauthz.ActionRead},
		{domainauthz.ResourceSession, domainauthz.ActionCreate},
		{domainauthz.ResourceSession, domainauthz.ActionUpdate},
		{domainauthz.ResourceSession, domainauthz.ActionDelete},
		{domainauthz.ResourceInvitation, domainauthz.ActionCreate},
		{domainauthz.ResourceInvitation, domainauthz.ActionRead},
	},
	"contributor": {
		{domainauthz.ResourceEvent, domainauthz.ActionRead},
		{domainauthz.ResourceMember, domainauthz.ActionRead},
		{domainauthz.ResourceRoom, domainauthz.ActionCreate},
		{domainauthz.ResourceRoom, domainauthz.ActionRead},
		{domainauthz.ResourceRoom, domainauthz.ActionUpdate},
		{domainauthz.ResourceRoom, domainauthz.ActionDelete},
		{domainauthz.ResourceSession, domainauthz.ActionRead},
		{domainauthz.ResourceSession, domainauthz.ActionCreate},
		{domainauthz.ResourceSession, domainauthz.ActionUpdate},
		{domainauthz.ResourceSession, domainauthz.ActionDelete},
		{domainauthz.ResourceInvitation, domainauthz.ActionCreate},
		{domainauthz.ResourceInvitation, domainauthz.ActionRead},
	},
	"attendee": {
		{domainauthz.ResourceEvent, domainauthz.ActionRead},
		{domainauthz.ResourceSession, domainauthz.ActionRead},
	},
}

const wantTotalRows = 31

// TestRolePermissionsSeed_RowCountMatchesFullyExpandedMatrix is the
// row-count-vs-matrix check: a dropped INSERT in the seed migration is a
// silent deny, not an error, so this must fail loud in CI instead.
func TestRolePermissionsSeed_RowCountMatchesFullyExpandedMatrix(t *testing.T) {
	pool := testPool(t)

	var gotCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM role_permissions`).Scan(&gotCount); err != nil {
		t.Fatalf("counting role_permissions: %v", err)
	}
	if gotCount != wantTotalRows {
		t.Errorf("role_permissions has %d rows, want %d (docs/requirements.md §4, fully expanded)", gotCount, wantTotalRows)
	}

	wantCount := 0
	for _, cells := range expectedMatrix {
		wantCount += len(cells)
	}
	if wantCount != wantTotalRows {
		t.Fatalf("test's own expectedMatrix has %d cells, want %d -- the test fixture itself drifted from docs/requirements.md §4", wantCount, wantTotalRows)
	}

	// Exact-set comparison, not just a count: prove every expected cell is
	// present and nothing extra was seeded.
	rows, err := pool.Query(context.Background(), `SELECT role, resource, action FROM role_permissions`)
	if err != nil {
		t.Fatalf("querying role_permissions: %v", err)
	}
	defer rows.Close()

	type cell struct {
		role, resource, action string
	}
	got := make(map[cell]bool)
	for rows.Next() {
		var c cell
		if err := rows.Scan(&c.role, &c.resource, &c.action); err != nil {
			t.Fatalf("scanning role_permissions row: %v", err)
		}
		got[c] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating role_permissions: %v", err)
	}

	want := make(map[cell]bool)
	for role, cells := range expectedMatrix {
		for _, c := range cells {
			want[cell{role, string(c.Resource), string(c.Action)}] = true
		}
	}

	for c := range want {
		if !got[c] {
			t.Errorf("role_permissions is missing expected row %+v", c)
		}
	}
	for c := range got {
		if !want[c] {
			t.Errorf("role_permissions has unexpected row %+v, not in docs/requirements.md §4", c)
		}
	}
}

// TestPolicy_Can_ReflectsMigrationSeededMatrix proves New() actually reads
// role_permissions from the database -- not a hand-rolled fixture standing
// in for it -- by checking every expectedMatrix cell against a live Policy,
// each on its own freshly seeded event+member.
func TestPolicy_Can_ReflectsMigrationSeededMatrix(t *testing.T) {
	pool := testPool(t)
	policy := newPolicy(t, pool)

	for role, cells := range expectedMatrix {
		for _, c := range cells {
			role, c := role, c
			t.Run(fmt.Sprintf("%s/%s:%s", role, c.Resource, c.Action), func(t *testing.T) {
				userID := seedUser(t, pool)
				eventID := seedEvent(t, pool)
				seedMember(t, pool, eventID, userID, role)

				allowed, gotRole, err := policy.Can(context.Background(), userID, eventID, c.Action, c.Resource)
				if err != nil {
					t.Fatalf("Can: %v", err)
				}
				if gotRole != role {
					t.Errorf("Can(%s, %s:%s) role = %q, want %q", role, c.Resource, c.Action, gotRole, role)
				}
				if !allowed {
					t.Errorf("Can(%s, %s:%s) = false, want true (docs/requirements.md §4)", role, c.Resource, c.Action)
				}
			})
		}
	}
}

// TestPolicy_Can_ContributorAssignRole_Returns403Denied is item 07's named
// must: test -- a contributor gets 403 attempting a role change. This
// proves it at the policy layer; the http-level 403 is proved in
// internal/http/middleware/authz_integration_test.go.
func TestPolicy_Can_ContributorAssignRole_ReturnsDenied(t *testing.T) {
	pool := testPool(t)
	policy := newPolicy(t, pool)

	userID := seedUser(t, pool)
	eventID := seedEvent(t, pool)
	seedMember(t, pool, eventID, userID, "contributor")

	allowed, gotRole, err := policy.Can(context.Background(), userID, eventID, domainauthz.ActionAssignRole, domainauthz.ResourceMember)
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if allowed {
		t.Error("Can(contributor, member:assign-role) = true, want false -- a contributor must never change roles")
	}
	if gotRole != "contributor" {
		t.Errorf("Can role = %q, want contributor -- role must be returned even when the action is denied", gotRole)
	}
}

func TestPolicy_Can_NonMemberOfRealEvent_ReturnsFalseNotError(t *testing.T) {
	pool := testPool(t)
	policy := newPolicy(t, pool)

	userID := seedUser(t, pool)
	eventID := seedEvent(t, pool) // no event_members row for userID

	allowed, gotRole, err := policy.Can(context.Background(), userID, eventID, domainauthz.ActionRead, domainauthz.ResourceEvent)
	if err != nil {
		t.Fatalf("Can: %v, want nil error for a non-member", err)
	}
	if allowed {
		t.Error("Can returned true for a user with no event_members row, want false")
	}
	if gotRole != "" {
		t.Errorf("Can role = %q, want empty string for a non-member", gotRole)
	}
}

// TestPolicy_Can_NonExistentEvent_ReturnsFalseSameAsNonMember is the
// deliberate, documented non-leak case from the plan: a non-existent event
// must answer identically to "real event, not a member" -- (false, nil) --
// so a caller with no standing can never learn whether the event exists.
func TestPolicy_Can_NonExistentEvent_ReturnsFalseSameAsNonMember(t *testing.T) {
	pool := testPool(t)
	policy := newPolicy(t, pool)

	userID := seedUser(t, pool)
	nonExistentEventID := randomUUIDv4()

	allowed, gotRole, err := policy.Can(context.Background(), userID, nonExistentEventID, domainauthz.ActionRead, domainauthz.ResourceEvent)
	if err != nil {
		t.Fatalf("Can: %v, want nil error for a non-existent event -- must not leak existence via an error either", err)
	}
	if allowed {
		t.Error("Can returned true for a non-existent event, want false")
	}
	if gotRole != "" {
		t.Errorf("Can role = %q, want empty string for a non-existent event", gotRole)
	}
}
