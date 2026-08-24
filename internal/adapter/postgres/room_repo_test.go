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
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
)

func intPtr(n int) *int { return &n }

func validRoomInput(name string) room.CreateInput {
	return room.CreateInput{Name: name, Capacity: intPtr(50)}
}

// insertValidSession inserts a constraint-valid sessions row referencing
// roomID directly via SQL -- item 12's domain code doesn't exist yet, but
// the sessions table does (migrations/20260823164421_init_schema.up.sql),
// and sessions.room_id has no ON DELETE action. The row must satisfy every
// NOT NULL, its speaker_id FK to a real users row, and
// sessions_time_range_bounded_check (a bounded, non-empty range) -- an
// invalid row would fail on that CHECK before ever reaching the room-delete
// FK path, proving nothing about the delete guard this exists to test.
func insertValidSession(t *testing.T, pool *pgxpool.Pool, eventID, roomID, speakerID string) {
	t.Helper()
	const insert = `
		INSERT INTO sessions (event_id, room_id, speaker_id, title, time_range)
		VALUES ($1, $2, $3, $4, tstzrange($5, $6, '[)'))`
	starts := time.Now().UTC().Truncate(time.Millisecond)
	ends := starts.Add(time.Hour)
	if _, err := pool.Exec(context.Background(), insert, eventID, roomID, speakerID, "Test session "+uniqueSubject(t), starts, ends); err != nil {
		t.Fatalf("inserting test session: %v", err)
	}
}

func TestRoomRepository_Create_CreatesRoomAndAudits(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	name := "Hall A " + uniqueSubject(t)

	got, err := repo.Create(ctx, creator, ev.ID, validRoomInput(name))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Fatal("got empty room ID")
	}
	if got.Name != name {
		t.Errorf("got Name %q, want %q", got.Name, name)
	}
	if got.EventID != ev.ID {
		t.Errorf("got EventID %q, want %q", got.EventID, ev.ID)
	}
	if got.Capacity == nil || *got.Capacity != 50 {
		t.Errorf("got Capacity %v, want 50", got.Capacity)
	}
	if got.Version != 1 {
		t.Errorf("got Version %d, want 1", got.Version)
	}

	var action string
	var after []byte
	err = pool.QueryRow(ctx, `SELECT action, after FROM audit_log WHERE entity_type = 'room' AND entity_id = $1`, got.ID).
		Scan(&action, &after)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if action != "create" {
		t.Errorf("got action %q, want create", action)
	}
	var afterMap map[string]any
	if err := json.Unmarshal(after, &afterMap); err != nil {
		t.Fatalf("unmarshaling audit after: %v", err)
	}
	if afterMap["name"] != name {
		t.Errorf("audit after.name = %v, want %q", afterMap["name"], name)
	}
}

func TestRoomRepository_Create_NullCapacity(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	got, err := repo.Create(ctx, creator, ev.ID, room.CreateInput{Name: "No capacity " + uniqueSubject(t)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Capacity != nil {
		t.Errorf("got Capacity %v, want nil", got.Capacity)
	}
}

func TestRoomRepository_Create_DuplicateNameInEvent_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	name := "Dup " + uniqueSubject(t)

	if _, err := repo.Create(ctx, creator, ev.ID, validRoomInput(name)); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := repo.Create(ctx, creator, ev.ID, validRoomInput(name))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want domain.ErrConflict", err)
	}
}

func TestRoomRepository_Create_SameNameDifferentEvent_Succeeds(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)
	name := "Shared name " + uniqueSubject(t)

	if _, err := repo.Create(ctx, creator, ev1.ID, validRoomInput(name)); err != nil {
		t.Fatalf("Create in event 1: %v", err)
	}
	if _, err := repo.Create(ctx, creator, ev2.ID, validRoomInput(name)); err != nil {
		t.Fatalf("Create in event 2 (same name, different event): %v", err)
	}
}

func TestRoomRepository_Get_ReturnsNotFoundForMissingRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Get(context.Background(), ev.ID, "01900000-0000-7000-8000-000000000000")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestRoomRepository_Get_WrongEvent_ReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)

	created, err := repo.Create(ctx, creator, ev1.ID, validRoomInput("Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.Get(ctx, ev2.ID, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get via wrong event: got err %v, want domain.ErrNotFound", err)
	}
}

// TestRoomRepository_List_PaginatesWithoutSkipOrDuplicate forces every
// created room onto the same created_at, per FAILURES.md's tie-break rule:
// distinct timestamps let a broken created_at-only ORDER BY/cursor pass
// anyway, since the tie never gets exercised.
func TestRoomRepository_List_PaginatesWithoutSkipOrDuplicate(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	want := make(map[string]bool)
	for i := 0; i < 4; i++ {
		created, err := repo.Create(ctx, creator, ev.ID, validRoomInput(fmt.Sprintf("List room %d %s", i, uniqueSubject(t))))
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		want[created.ID] = true
	}

	tiedCreatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	if _, err := pool.Exec(ctx, `UPDATE rooms SET created_at = $1 WHERE id = ANY($2)`, tiedCreatedAt, ids); err != nil {
		t.Fatalf("forcing a created_at tie: %v", err)
	}

	got := make(map[string]bool)

	page1, err := repo.List(ctx, ev.ID, 2, nil)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page1.Rooms) != 2 {
		t.Fatalf("page 1 got %d rooms, want 2", len(page1.Rooms))
	}
	if page1.NextCursor == nil {
		t.Fatal("page 1 got nil NextCursor, want one (more rooms remain)")
	}
	for _, rm := range page1.Rooms {
		if !rm.CreatedAt.Equal(tiedCreatedAt) {
			t.Fatalf("room %q has created_at %v, want the forced tie %v -- the tie setup didn't take", rm.ID, rm.CreatedAt, tiedCreatedAt)
		}
		got[rm.ID] = true
	}

	page2, err := repo.List(ctx, ev.ID, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page2.Rooms) != 2 {
		t.Fatalf("page 2 got %d rooms, want 2", len(page2.Rooms))
	}
	if page2.NextCursor != nil {
		t.Fatal("page 2 got a NextCursor, want nil (no further page)")
	}
	for _, rm := range page2.Rooms {
		got[rm.ID] = true
	}

	if len(got) != len(want) {
		t.Fatalf("got %d distinct rooms across both pages, want %d (no skips or duplicates)", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("room %q missing from combined pages", id)
		}
	}
}

func TestRoomRepository_List_ScopesToEvent(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)

	if _, err := repo.Create(ctx, creator, ev1.ID, validRoomInput("In ev1 "+uniqueSubject(t))); err != nil {
		t.Fatalf("Create (ev1): %v", err)
	}
	if _, err := repo.Create(ctx, creator, ev2.ID, validRoomInput("In ev2 "+uniqueSubject(t))); err != nil {
		t.Fatalf("Create (ev2): %v", err)
	}

	page, err := repo.List(ctx, ev1.ID, 10, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Rooms) != 1 {
		t.Fatalf("got %d rooms for ev1, want exactly the 1 that belongs to it", len(page.Rooms))
	}
}

func TestRoomRepository_Update_SucceedsAndAudits(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Original "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "Renamed " + uniqueSubject(t)
	got, err := repo.Update(ctx, creator, ev.ID, created.ID, created.Version, room.Patch{Name: opt.Of(newName)})
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
		`SELECT before, after FROM audit_log WHERE entity_type = 'room' AND entity_id = $1 AND action = 'update' ORDER BY created_at DESC LIMIT 1`,
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

// TestRoomRepository_Update_CapacityThreeStates proves opt.Optional[*int]
// keeps "don't touch", "clear to null", and "set a value" as three distinct
// outcomes -- the first field in the codebase combining PATCH-optionality
// with a numeric CHECK.
func TestRoomRepository_Update_CapacityThreeStates(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Capacity room "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Capacity == nil || *created.Capacity != 50 {
		t.Fatalf("fixture got Capacity %v, want 50", created.Capacity)
	}

	// (1) Absent -- capacity untouched.
	untouched, err := repo.Update(ctx, creator, ev.ID, created.ID, created.Version, room.Patch{Name: opt.Of("Renamed once " + uniqueSubject(t))})
	if err != nil {
		t.Fatalf("Update (absent capacity): %v", err)
	}
	if untouched.Capacity == nil || *untouched.Capacity != 50 {
		t.Fatalf("got Capacity %v after an update that didn't set it, want unchanged 50", untouched.Capacity)
	}

	// (2) Explicit null -- capacity cleared.
	cleared, err := repo.Update(ctx, creator, ev.ID, created.ID, untouched.Version, room.Patch{Capacity: opt.Of[*int](nil)})
	if err != nil {
		t.Fatalf("Update (null capacity): %v", err)
	}
	if cleared.Capacity != nil {
		t.Fatalf("got Capacity %v after an explicit null, want nil", cleared.Capacity)
	}

	// (3) Explicit value -- capacity set.
	set, err := repo.Update(ctx, creator, ev.ID, created.ID, cleared.Version, room.Patch{Capacity: opt.Of(intPtr(75))})
	if err != nil {
		t.Fatalf("Update (set capacity): %v", err)
	}
	if set.Capacity == nil || *set.Capacity != 75 {
		t.Fatalf("got Capacity %v after setting 75, want 75", set.Capacity)
	}
}

func TestRoomRepository_Update_StaleVersionReturnsVersionMismatch(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Stale "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev.ID, created.ID, created.Version+99, room.Patch{Name: opt.Of("Should not apply")})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want domain.ErrVersionMismatch", err)
	}

	current, err := repo.Get(ctx, ev.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Name != created.Name {
		t.Errorf("room was mutated despite version mismatch: got Name %q, want unchanged %q", current.Name, created.Name)
	}
}

func TestRoomRepository_Update_MissingRowReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Update(context.Background(), creator, ev.ID, "01900000-0000-7000-8000-000000000000", 1, room.Patch{Name: opt.Of("x")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestRoomRepository_Update_WrongEventReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)

	created, err := repo.Create(ctx, creator, ev1.ID, validRoomInput("Ev1 room "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev2.ID, created.ID, created.Version, room.Patch{Name: opt.Of("Should not apply")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update via wrong event: got err %v, want domain.ErrNotFound", err)
	}
}

func TestRoomRepository_Update_RenameToExistingNameInEvent_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	taken := "Taken " + uniqueSubject(t)
	if _, err := repo.Create(ctx, creator, ev.ID, validRoomInput(taken)); err != nil {
		t.Fatalf("Create (taken name): %v", err)
	}
	toRename, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Renaming "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create (to rename): %v", err)
	}

	_, err = repo.Update(ctx, creator, ev.ID, toRename.ID, toRename.Version, room.Patch{Name: opt.Of(taken)})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want domain.ErrConflict", err)
	}
}

func TestRoomRepository_Delete_HardDeletesAndAudits(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("To delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, creator, ev.ID, created.ID, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := countRows(t, pool, `SELECT count(*) FROM rooms WHERE id = $1`, created.ID); n != 0 {
		t.Errorf("rooms rows for id after delete = %d, want 0 (a real hard delete, not soft)", n)
	}

	if _, err := repo.Get(ctx, ev.ID, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after delete: got err %v, want domain.ErrNotFound", err)
	}

	var action string
	var after []byte
	err = pool.QueryRow(ctx, `SELECT action, after FROM audit_log WHERE entity_type = 'room' AND entity_id = $1 AND action = 'delete'`, created.ID).
		Scan(&action, &after)
	if err != nil {
		t.Fatalf("querying delete audit row: %v", err)
	}
	if after != nil {
		t.Errorf("delete audit after = %s, want NULL", after)
	}
}

func TestRoomRepository_Delete_StaleVersionReturnsVersionMismatch(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("Stale delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = repo.Delete(ctx, creator, ev.ID, created.ID, created.Version+99)
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want domain.ErrVersionMismatch", err)
	}

	if _, err := repo.Get(ctx, ev.ID, created.ID); err != nil {
		t.Fatalf("Get after failed delete: got err %v, want the room to still exist", err)
	}
}

func TestRoomRepository_Delete_MissingRowReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	err := repo.Delete(context.Background(), creator, ev.ID, "01900000-0000-7000-8000-000000000000", 1)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestRoomRepository_Delete_WrongEventReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)

	created, err := repo.Create(ctx, creator, ev1.ID, validRoomInput("Ev1 room "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = repo.Delete(ctx, creator, ev2.ID, created.ID, created.Version)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete via wrong event: got err %v, want domain.ErrNotFound", err)
	}
	if _, err := repo.Get(ctx, ev1.ID, created.ID); err != nil {
		t.Fatalf("room should still exist under its real event: %v", err)
	}
}

// TestRoomRepository_Delete_RoomWithSessions_ReturnsConflict proves the
// 23503 foreign_key_violation -> domain.ErrConflict mapping ahead of item
// 12 (Sessions CRUD) landing: sessions.room_id has no ON DELETE action, so
// a room with a session still referencing it can't be deleted. Product
// decision: no cascade -- the caller must remove/reassign the room's
// sessions first.
func TestRoomRepository_Delete_RoomWithSessions_ReturnsConflict(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewRoomRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	created, err := repo.Create(ctx, creator, ev.ID, validRoomInput("In use "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	insertValidSession(t, pool, ev.ID, created.ID, speaker)

	err = repo.Delete(ctx, creator, ev.ID, created.ID, created.Version)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got err %v, want domain.ErrConflict", err)
	}

	if n := countRows(t, pool, `SELECT count(*) FROM rooms WHERE id = $1`, created.ID); n != 1 {
		t.Errorf("rooms rows for id after blocked delete = %d, want 1 (delete must not have partially applied)", n)
	}
}
