package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

// createTestUser inserts a users row directly -- event_repo tests need an
// actor to attribute audit rows to, but exercising the full OIDC/auth stack
// for that is user_repo_test.go's and the middleware integration tests' job,
// not this one's.
func createTestUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	subject := uniqueSubject(t)
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (subject, email, name) VALUES ($1, $2, $3) RETURNING id::text`,
		subject, subject+"@test.krane", "Test User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("inserting test user: %v", err)
	}
	return id
}

func validCreateInput(name string) event.CreateInput {
	return event.CreateInput{
		Name:     name,
		Timezone: "Asia/Colombo",
		StartsAt: time.Now().UTC().Truncate(time.Millisecond),
		EndsAt:   time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond),
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("counting rows (%s): %v", query, err)
	}
	return n
}

func TestEventRepository_Create_CreatesEventAdminMembershipAndTwoAuditRows(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	name := "Conf " + uniqueSubject(t)

	got, err := repo.Create(ctx, creator, validCreateInput(name))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Fatal("got empty event ID")
	}
	if got.Name != name {
		t.Errorf("got Name %q, want %q", got.Name, name)
	}
	if got.Version != 1 {
		t.Errorf("got Version %d, want 1", got.Version)
	}

	if n := countRows(t, pool, `SELECT count(*) FROM events WHERE id = $1`, got.ID); n != 1 {
		t.Errorf("events rows for id = %d, want 1", n)
	}

	var memberID, role string
	err = pool.QueryRow(ctx, `SELECT id::text, role FROM event_members WHERE event_id = $1 AND user_id = $2`, got.ID, creator).Scan(&memberID, &role)
	if err != nil {
		t.Fatalf("querying event_members: %v", err)
	}
	if role != "admin" {
		t.Errorf("got role %q, want admin", role)
	}

	rows, err := pool.Query(ctx, `SELECT entity_type, entity_id::text, action, after FROM audit_log WHERE actor_id = $1 ORDER BY entity_type`, creator)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	defer rows.Close()

	var gotTypes []string
	for rows.Next() {
		var entityType, entityID, action string
		var after []byte
		if err := rows.Scan(&entityType, &entityID, &action, &after); err != nil {
			t.Fatalf("scanning audit_log row: %v", err)
		}
		if action != "create" {
			t.Errorf("got action %q, want create", action)
		}
		gotTypes = append(gotTypes, entityType)

		// The event_member audit row's entity_id must be the membership
		// row's OWN primary key -- not the event's id -- since this is
		// exactly the shape item 09's explicit add-member reuses.
		switch entityType {
		case "event":
			if entityID != got.ID {
				t.Errorf("event audit row entity_id = %q, want the event's id %q", entityID, got.ID)
			}
		case "event_member":
			if entityID != memberID {
				t.Errorf("event_member audit row entity_id = %q, want the membership row's own id %q (not the event's id)", entityID, memberID)
			}
			var afterMap map[string]any
			if err := json.Unmarshal(after, &afterMap); err != nil {
				t.Fatalf("unmarshaling event_member audit after: %v", err)
			}
			if afterMap["role"] != "admin" {
				t.Errorf("event_member audit after.role = %v, want admin", afterMap["role"])
			}
		}
	}
	want := []string{"event", "event_member"}
	if fmt.Sprint(gotTypes) != fmt.Sprint(want) {
		t.Errorf("got audit entity_types %v, want %v", gotTypes, want)
	}
}

func TestEventRepository_Create_ValidationFailureRollsBackEverything(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	name := "Broken " + uniqueSubject(t)

	in := validCreateInput(name)
	in.EndsAt = in.StartsAt.Add(-time.Hour) // violates events_ends_after_starts_check

	if _, err := repo.Create(ctx, creator, in); err == nil {
		t.Fatal("Create: got nil error, want a constraint violation")
	}

	if n := countRows(t, pool, `SELECT count(*) FROM events WHERE name = $1`, name); n != 0 {
		t.Errorf("events rows for %q = %d, want 0 (transaction should have rolled back)", name, n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM event_members WHERE user_id = $1`, creator); n != 0 {
		t.Errorf("event_members rows for creator = %d, want 0", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM audit_log WHERE actor_id = $1`, creator); n != 0 {
		t.Errorf("audit_log rows for creator = %d, want 0", n)
	}
}

func TestEventRepository_Get_ReturnsNotFoundForMissingRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)

	_, err := repo.Get(context.Background(), "01900000-0000-7000-8000-000000000000")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestEventRepository_Get_ReturnsNotFoundForSoftDeletedRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	created, err := repo.Create(ctx, creator, validCreateInput("To delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.Delete(ctx, creator, created.ID, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after delete: got err %v, want domain.ErrNotFound", err)
	}
}

// TestEventRepository_List_PaginatesWithoutSkipOrDuplicate forces every
// created event to share the exact same created_at timestamp -- if List
// ordered by created_at alone (dropping id as the tie-breaker), this would
// either skip or duplicate rows across pages. Ordering by (created_at, id)
// together, with id as the deterministic tie-break, is what this proves;
// distinct created_at values (the common case) would pass even with a
// broken created_at-only implementation, so the tie is deliberate.
func TestEventRepository_List_PaginatesWithoutSkipOrDuplicate(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)

	want := make(map[string]bool)
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("List event %d %s", i, uniqueSubject(t))
		created, err := repo.Create(ctx, creator, validCreateInput(name))
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		want[created.ID] = true
	}

	// Collapse all four rows onto one identical created_at -- id is now the
	// only thing that can order or tie-break them.
	tiedCreatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	if _, err := pool.Exec(ctx, `UPDATE events SET created_at = $1 WHERE id = ANY($2)`, tiedCreatedAt, ids); err != nil {
		t.Fatalf("forcing a created_at tie: %v", err)
	}

	got := make(map[string]bool)

	page1, err := repo.List(ctx, creator, 2, nil)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page1.Events) != 2 {
		t.Fatalf("page 1 got %d events, want 2", len(page1.Events))
	}
	if page1.NextCursor == nil {
		t.Fatal("page 1 got nil NextCursor, want one (more events remain)")
	}
	for _, e := range page1.Events {
		if !e.CreatedAt.Equal(tiedCreatedAt) {
			t.Fatalf("event %q has created_at %v, want the forced tie %v -- the tie setup didn't take", e.ID, e.CreatedAt, tiedCreatedAt)
		}
		got[e.ID] = true
	}

	page2, err := repo.List(ctx, creator, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page2.Events) != 2 {
		t.Fatalf("page 2 got %d events, want 2", len(page2.Events))
	}
	if page2.NextCursor != nil {
		t.Fatal("page 2 got a NextCursor, want nil (no further page)")
	}
	for _, e := range page2.Events {
		got[e.ID] = true
	}

	if len(got) != len(want) {
		t.Fatalf("got %d distinct events across both pages, want %d (no skips or duplicates)", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("event %q missing from combined pages", id)
		}
	}
}

func TestEventRepository_List_ScopesToMembership(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	member := createTestUser(t, pool)
	other := createTestUser(t, pool)

	if _, err := repo.Create(ctx, member, validCreateInput("Mine "+uniqueSubject(t))); err != nil {
		t.Fatalf("Create (member's event): %v", err)
	}
	if _, err := repo.Create(ctx, other, validCreateInput("Not mine "+uniqueSubject(t))); err != nil {
		t.Fatalf("Create (other's event): %v", err)
	}

	page, err := repo.List(ctx, member, 10, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("got %d events for member, want exactly the 1 they belong to", len(page.Events))
	}
}

func TestEventRepository_Update_SucceedsAndAudits(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	created, err := repo.Create(ctx, creator, validCreateInput("Original "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "Renamed " + uniqueSubject(t)
	got, err := repo.Update(ctx, creator, created.ID, created.Version, event.Patch{Name: opt.Of(newName)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != newName {
		t.Errorf("got Name %q, want %q", got.Name, newName)
	}
	if got.Version != created.Version+1 {
		t.Errorf("got Version %d, want %d", got.Version, created.Version+1)
	}

	var beforeRaw, afterRaw []byte
	err = pool.QueryRow(ctx,
		`SELECT before, after FROM audit_log WHERE entity_id = $1 AND action = 'update' ORDER BY created_at DESC LIMIT 1`,
		created.ID,
	).Scan(&beforeRaw, &afterRaw)
	if err != nil {
		t.Fatalf("querying update audit row: %v", err)
	}

	var before, after map[string]any
	if err := json.Unmarshal(beforeRaw, &before); err != nil {
		t.Fatalf("unmarshaling before: %v", err)
	}
	if err := json.Unmarshal(afterRaw, &after); err != nil {
		t.Fatalf("unmarshaling after: %v", err)
	}
	if before["name"] == after["name"] {
		t.Errorf("audit before/after name unchanged (%v); want before != after", before["name"])
	}
}

func TestEventRepository_Update_StaleVersionReturnsVersionMismatch(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	created, err := repo.Create(ctx, creator, validCreateInput("Stale "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Update(ctx, creator, created.ID, created.Version+99, event.Patch{Name: opt.Of("Should not apply")})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want domain.ErrVersionMismatch", err)
	}

	current, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Name != created.Name {
		t.Errorf("event was mutated despite version mismatch: got Name %q, want unchanged %q", current.Name, created.Name)
	}
}

func TestEventRepository_Update_MissingRowReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	creator := createTestUser(t, pool)

	_, err := repo.Update(context.Background(), creator, "01900000-0000-7000-8000-000000000000", 1, event.Patch{Name: opt.Of("x")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestEventRepository_Delete_SoftDeletesExcludesFromGetAndListAndAudits(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	created, err := repo.Create(ctx, creator, validCreateInput("To delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.Delete(ctx, creator, created.ID, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at FROM events WHERE id = $1`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("querying events.deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("events.deleted_at is NULL, want it set")
	}

	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after delete: got err %v, want domain.ErrNotFound", err)
	}

	page, err := repo.List(ctx, creator, 10, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range page.Events {
		if e.ID == created.ID {
			t.Fatalf("deleted event %q still appears in List", created.ID)
		}
	}

	if n := countRows(t, pool, `SELECT count(*) FROM audit_log WHERE entity_id = $1 AND action = 'delete'`, created.ID); n != 1 {
		t.Errorf("got %d delete audit rows, want 1", n)
	}
}

func TestEventRepository_Delete_StaleVersionReturnsVersionMismatch(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	created, err := repo.Create(ctx, creator, validCreateInput("Stale delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Delete(ctx, creator, created.ID, created.Version+99)
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want domain.ErrVersionMismatch", err)
	}

	if _, err := repo.Get(ctx, created.ID); err != nil {
		t.Fatalf("Get after failed delete: got err %v, want the event to still exist", err)
	}
}

// TestEventRepository_Update_DescriptionThreeStates is events' copy of the
// assignment's "clearing a field and leaving it untouched are different
// requests" proof at the persistence layer -- sessions and rooms already had
// theirs (session_repo_test.go / room_repo_test.go); events' nullable
// description was proven only at request validation until feature 29.
// Absent -> untouched, explicit null -> NULL, value -> stored; the version
// column advances on every write regardless of which fields it carried.
func TestEventRepository_Update_DescriptionThreeStates(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewEventRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	in := validCreateInput("Description states " + uniqueSubject(t))
	original := "Original description"
	in.Description = &original
	created, err := repo.Create(ctx, creator, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Description == nil || *created.Description != original {
		t.Fatalf("fixture: got Description %v, want %q", created.Description, original)
	}

	// (1) Absent -- description untouched, other field changed.
	untouched, err := repo.Update(ctx, creator, created.ID, created.Version, event.Patch{Name: opt.Of("Renamed once " + uniqueSubject(t))})
	if err != nil {
		t.Fatalf("Update (absent description): %v", err)
	}
	if untouched.Description == nil || *untouched.Description != original {
		t.Fatalf("got Description %v after an update that didn't set it, want unchanged %q", untouched.Description, original)
	}
	if untouched.Version != created.Version+1 {
		t.Fatalf("got Version %d after first update, want %d", untouched.Version, created.Version+1)
	}

	// (2) Explicit null -- description cleared.
	cleared, err := repo.Update(ctx, creator, created.ID, untouched.Version, event.Patch{Description: opt.Of[*string](nil)})
	if err != nil {
		t.Fatalf("Update (null description): %v", err)
	}
	if cleared.Description != nil {
		t.Fatalf("got Description %q after an explicit null, want nil", *cleared.Description)
	}
	if cleared.Version != untouched.Version+1 {
		t.Fatalf("got Version %d after clearing, want %d", cleared.Version, untouched.Version+1)
	}

	// (3) Explicit value -- description set.
	newDesc := "New description"
	set, err := repo.Update(ctx, creator, created.ID, cleared.Version, event.Patch{Description: opt.Of(&newDesc)})
	if err != nil {
		t.Fatalf("Update (set description): %v", err)
	}
	if set.Description == nil || *set.Description != newDesc {
		t.Fatalf("got Description %v after setting %q, want %q", set.Description, newDesc, newDesc)
	}

	// The re-read agrees with what Update returned -- RETURNING is not lying.
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description == nil || *got.Description != newDesc || got.Version != set.Version {
		t.Fatalf("re-read got Description %v / Version %d, want %q / %d", got.Description, got.Version, newDesc, set.Version)
	}
}
