package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

func validSessionInput(roomID, speakerID, title string) session.CreateInput {
	starts := time.Now().UTC().Truncate(time.Millisecond)
	return session.CreateInput{
		RoomID:    roomID,
		SpeakerID: speakerID,
		Title:     title,
		StartsAt:  starts,
		EndsAt:    starts.Add(time.Hour),
	}
}

func TestSessionRepository_Create_CreatesSessionAndAudits(t *testing.T) {
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

	title := "Keynote " + uniqueSubject(t)
	in := validSessionInput(rm.ID, speaker, title)
	got, err := repo.Create(ctx, creator, ev.ID, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Fatal("got empty session ID")
	}
	if got.EventID != ev.ID || got.RoomID != rm.ID || got.SpeakerID != speaker {
		t.Errorf("got EventID=%q RoomID=%q SpeakerID=%q, want %q/%q/%q", got.EventID, got.RoomID, got.SpeakerID, ev.ID, rm.ID, speaker)
	}
	if got.Title != title {
		t.Errorf("got Title %q, want %q", got.Title, title)
	}
	if !got.StartsAt.Equal(in.StartsAt) || !got.EndsAt.Equal(in.EndsAt) {
		t.Errorf("got StartsAt/EndsAt %v/%v, want %v/%v", got.StartsAt, got.EndsAt, in.StartsAt, in.EndsAt)
	}
	if got.Version != 1 {
		t.Errorf("got Version %d, want 1", got.Version)
	}

	var action string
	var after []byte
	err = pool.QueryRow(ctx, `SELECT action, after FROM audit_log WHERE entity_type = 'session' AND entity_id = $1`, got.ID).
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
	if afterMap["title"] != title {
		t.Errorf("audit after.title = %v, want %q", afterMap["title"], title)
	}
}

func TestSessionRepository_Create_WithDescription(t *testing.T) {
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

	in := validSessionInput(rm.ID, speaker, "With description "+uniqueSubject(t))
	desc := "A talk about things"
	in.Description = &desc

	got, err := repo.Create(ctx, creator, ev.ID, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Description == nil || *got.Description != desc {
		t.Errorf("got Description %v, want %q", got.Description, desc)
	}
}

// TestSessionRepository_Create_RoomInDifferentEvent_ReturnsErrInvalidRoom
// proves the atomic INSERT...SELECT room lookup, not a separate
// check-then-act SELECT, is what rejects a cross-event room reference.
func TestSessionRepository_Create_RoomInDifferentEvent_ReturnsErrInvalidRoom(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)
	rmInEv2, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev2.ID, validRoomInput("Ev2 room "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}

	_, err = repo.Create(ctx, creator, ev1.ID, validSessionInput(rmInEv2.ID, speaker, "Cross-event "+uniqueSubject(t)))
	if !errors.Is(err, session.ErrInvalidRoom) {
		t.Fatalf("got err %v, want session.ErrInvalidRoom", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM sessions WHERE event_id = $1`, ev1.ID); n != 0 {
		t.Errorf("sessions rows for ev1 = %d, want 0 (create must not have partially applied)", n)
	}
}

func TestSessionRepository_Create_MissingRoom_ReturnsErrInvalidRoom(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Create(ctx, creator, ev.ID, validSessionInput("01900000-0000-7000-8000-000000000000", speaker, "No room "+uniqueSubject(t)))
	if !errors.Is(err, session.ErrInvalidRoom) {
		t.Fatalf("got err %v, want session.ErrInvalidRoom", err)
	}
}

func TestSessionRepository_Create_MissingSpeaker_ReturnsErrInvalidSpeaker(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev.ID, validRoomInput("Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}

	_, err = repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, "01900000-0000-7000-8000-000000000000", "No speaker "+uniqueSubject(t)))
	if !errors.Is(err, session.ErrInvalidSpeaker) {
		t.Fatalf("got err %v, want session.ErrInvalidSpeaker", err)
	}
}

// TestSessionRepository_Create_BothRoomAndSpeakerInvalid_ReturnsInvalidRoom
// proves the documented precedence: the room lookup gates the INSERT's
// SELECT, so when the room doesn't resolve, the speaker's foreign key is
// never evaluated at all -- the error is always ErrInvalidRoom, never
// ErrInvalidSpeaker, when both are wrong.
func TestSessionRepository_Create_BothRoomAndSpeakerInvalid_ReturnsInvalidRoom(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Create(ctx, creator, ev.ID,
		validSessionInput("01900000-0000-7000-8000-000000000000", "01900000-0000-7000-8000-000000000001", "Both invalid "+uniqueSubject(t)))
	if !errors.Is(err, session.ErrInvalidRoom) {
		t.Fatalf("got err %v, want session.ErrInvalidRoom", err)
	}
	if errors.Is(err, session.ErrInvalidSpeaker) {
		t.Fatalf("got err also matching session.ErrInvalidSpeaker; precedence must be exclusively ErrInvalidRoom")
	}
}

func TestSessionRepository_Get_ReturnsNotFoundForMissingRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Get(context.Background(), ev.ID, "01900000-0000-7000-8000-000000000000")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestSessionRepository_Get_WrongEvent_ReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev1.ID, validRoomInput("Hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}

	created, err := repo.Create(ctx, creator, ev1.ID, validSessionInput(rm.ID, speaker, "Ev1 session "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.Get(ctx, ev2.ID, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get via wrong event: got err %v, want domain.ErrNotFound", err)
	}
}

func TestSessionRepository_Get_SoftDeleted_ReturnsNotFound(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "To delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.Delete(ctx, creator, ev.ID, created.ID, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.Get(ctx, ev.ID, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after soft delete: got err %v, want domain.ErrNotFound", err)
	}
}

// TestSessionRepository_List_PaginatesWithoutSkipOrDuplicate forces every
// created session onto the same created_at, per FAILURES.md's tie-break
// rule: distinct timestamps let a broken created_at-only ORDER BY/cursor
// pass anyway, since the tie never gets exercised.
func TestSessionRepository_List_PaginatesWithoutSkipOrDuplicate(t *testing.T) {
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

	want := make(map[string]bool)
	for i := 0; i < 4; i++ {
		created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, fmt.Sprintf("List session %d %s", i, uniqueSubject(t))))
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
	if _, err := pool.Exec(ctx, `UPDATE sessions SET created_at = $1 WHERE id = ANY($2)`, tiedCreatedAt, ids); err != nil {
		t.Fatalf("forcing a created_at tie: %v", err)
	}

	got := make(map[string]bool)

	page1, err := repo.List(ctx, ev.ID, 2, nil)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page1.Sessions) != 2 {
		t.Fatalf("page 1 got %d sessions, want 2", len(page1.Sessions))
	}
	if page1.NextCursor == nil {
		t.Fatal("page 1 got nil NextCursor, want one (more sessions remain)")
	}
	for _, s := range page1.Sessions {
		if !s.CreatedAt.Equal(tiedCreatedAt) {
			t.Fatalf("session %q has created_at %v, want the forced tie %v -- the tie setup didn't take", s.ID, s.CreatedAt, tiedCreatedAt)
		}
		got[s.ID] = true
	}

	page2, err := repo.List(ctx, ev.ID, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page2.Sessions) != 2 {
		t.Fatalf("page 2 got %d sessions, want 2", len(page2.Sessions))
	}
	if page2.NextCursor != nil {
		t.Fatal("page 2 got a NextCursor, want nil (no further page)")
	}
	for _, s := range page2.Sessions {
		got[s.ID] = true
	}

	if len(got) != len(want) {
		t.Fatalf("got %d distinct sessions across both pages, want %d (no skips or duplicates)", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("session %q missing from combined pages", id)
		}
	}
}

func TestSessionRepository_List_ScopesToEventAndExcludesSoftDeleted(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)
	rm1, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev1.ID, validRoomInput("Ev1 hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}
	rm2, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev2.ID, validRoomInput("Ev2 hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}

	if _, err := repo.Create(ctx, creator, ev2.ID, validSessionInput(rm2.ID, speaker, "In ev2 "+uniqueSubject(t))); err != nil {
		t.Fatalf("Create (ev2): %v", err)
	}
	kept, err := repo.Create(ctx, creator, ev1.ID, validSessionInput(rm1.ID, speaker, "Kept "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create (kept): %v", err)
	}
	deleted, err := repo.Create(ctx, creator, ev1.ID, validSessionInput(rm1.ID, speaker, "Deleted "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create (to delete): %v", err)
	}
	if _, err := repo.Delete(ctx, creator, ev1.ID, deleted.ID, deleted.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	page, err := repo.List(ctx, ev1.ID, 10, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].ID != kept.ID {
		t.Fatalf("got %d sessions (%+v), want exactly the 1 live session in ev1", len(page.Sessions), page.Sessions)
	}
}

func TestSessionRepository_Update_SucceedsAndAudits(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Original "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTitle := "Renamed " + uniqueSubject(t)
	got, err := repo.Update(ctx, creator, ev.ID, created.ID, created.Version, session.Patch{Title: opt.Of(newTitle)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Title != newTitle {
		t.Errorf("got Title %q, want %q", got.Title, newTitle)
	}
	if got.Version != created.Version+1 {
		t.Errorf("got Version %d, want %d", got.Version, created.Version+1)
	}
	// room_id/speaker_id are not patchable -- confirm they survive untouched.
	if got.RoomID != rm.ID || got.SpeakerID != speaker {
		t.Errorf("got RoomID=%q SpeakerID=%q, want unchanged %q/%q", got.RoomID, got.SpeakerID, rm.ID, speaker)
	}

	var beforeRaw, afterRaw []byte
	err = pool.QueryRow(ctx,
		`SELECT before, after FROM audit_log WHERE entity_type = 'session' AND entity_id = $1 AND action = 'update' ORDER BY created_at DESC LIMIT 1`,
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
	if before["title"] == after["title"] {
		t.Errorf("audit before/after title unchanged (%v); want before != after", before["title"])
	}
}

// TestSessionRepository_Update_DescriptionThreeStates proves
// opt.Optional[*string] keeps "don't touch", "clear to null", and "set a
// value" as three distinct outcomes -- the Optional[T] PATCH proof case
// docs/requirements.md names for this table (item 20 owns the rigorous
// cross-endpoint version; this is the basic per-feature proof, matching
// room.Capacity's item-11 precedent).
func TestSessionRepository_Update_DescriptionThreeStates(t *testing.T) {
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
	in := validSessionInput(rm.ID, speaker, "Description states "+uniqueSubject(t))
	original := "Original description"
	in.Description = &original
	created, err := repo.Create(ctx, creator, ev.ID, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// (1) Absent -- description untouched.
	untouched, err := repo.Update(ctx, creator, ev.ID, created.ID, created.Version, session.Patch{Title: opt.Of("Renamed once " + uniqueSubject(t))})
	if err != nil {
		t.Fatalf("Update (absent description): %v", err)
	}
	if untouched.Description == nil || *untouched.Description != original {
		t.Fatalf("got Description %v after an update that didn't set it, want unchanged %q", untouched.Description, original)
	}

	// (2) Explicit null -- description cleared.
	cleared, err := repo.Update(ctx, creator, ev.ID, created.ID, untouched.Version, session.Patch{Description: opt.Of[*string](nil)})
	if err != nil {
		t.Fatalf("Update (null description): %v", err)
	}
	if cleared.Description != nil {
		t.Fatalf("got Description %v after an explicit null, want nil", cleared.Description)
	}

	// (3) Explicit value -- description set.
	newDesc := "New description"
	set, err := repo.Update(ctx, creator, ev.ID, created.ID, cleared.Version, session.Patch{Description: opt.Of(&newDesc)})
	if err != nil {
		t.Fatalf("Update (set description): %v", err)
	}
	if set.Description == nil || *set.Description != newDesc {
		t.Fatalf("got Description %v after setting %q, want %q", set.Description, newDesc, newDesc)
	}
}

func TestSessionRepository_Update_StartsAtEndsAtTogether(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Reschedule "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newStarts := created.StartsAt.Add(24 * time.Hour)
	newEnds := newStarts.Add(2 * time.Hour)
	got, err := repo.Update(ctx, creator, ev.ID, created.ID, created.Version, session.Patch{
		StartsAt: opt.Of(newStarts),
		EndsAt:   opt.Of(newEnds),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !got.StartsAt.Equal(newStarts) || !got.EndsAt.Equal(newEnds) {
		t.Errorf("got StartsAt/EndsAt %v/%v, want %v/%v", got.StartsAt, got.EndsAt, newStarts, newEnds)
	}
}

func TestSessionRepository_Update_StaleVersionReturnsVersionMismatch(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Stale "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev.ID, created.ID, created.Version+99, session.Patch{Title: opt.Of("Should not apply")})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want domain.ErrVersionMismatch", err)
	}

	current, err := repo.Get(ctx, ev.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Title != created.Title {
		t.Errorf("session was mutated despite version mismatch: got Title %q, want unchanged %q", current.Title, created.Title)
	}
}

func TestSessionRepository_Update_MissingRowReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Update(context.Background(), creator, ev.ID, "01900000-0000-7000-8000-000000000000", 1, session.Patch{Title: opt.Of("x")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestSessionRepository_Update_WrongEventReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev1.ID, validRoomInput("Ev1 hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}
	created, err := repo.Create(ctx, creator, ev1.ID, validSessionInput(rm.ID, speaker, "Ev1 session "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev2.ID, created.ID, created.Version, session.Patch{Title: opt.Of("Should not apply")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update via wrong event: got err %v, want domain.ErrNotFound", err)
	}
}

func TestSessionRepository_Update_SoftDeletedReturnsNotFound(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "To delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Delete(ctx, creator, ev.ID, created.ID, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = repo.Update(ctx, creator, ev.ID, created.ID, created.Version+1, session.Patch{Title: opt.Of("Should not apply")})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update on soft-deleted session: got err %v, want domain.ErrNotFound", err)
	}
}

func TestSessionRepository_Delete_SoftDeletesAndAudits(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "To delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.Delete(ctx, creator, ev.ID, created.ID, created.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at FROM sessions WHERE id = $1`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("querying deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("deleted_at is NULL, want it set -- this must be a soft delete, matching events, not rooms")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM sessions WHERE id = $1`, created.ID); n != 1 {
		t.Errorf("sessions rows for id after delete = %d, want 1 (a soft delete, the row must still exist)", n)
	}

	var action string
	var after []byte
	err = pool.QueryRow(ctx, `SELECT action, after FROM audit_log WHERE entity_type = 'session' AND entity_id = $1 AND action = 'delete'`, created.ID).
		Scan(&action, &after)
	if err != nil {
		t.Fatalf("querying delete audit row: %v", err)
	}
	// Unlike rooms' hard delete, a session's soft delete is an UPDATE -- the
	// row still exists afterward (with deleted_at set), so "after" is
	// populated, matching events' soft-delete audit convention.
	if after == nil {
		t.Fatal("delete audit after is NULL, want the post-delete row (a soft delete, matching events)")
	}
	var afterMap map[string]any
	if err := json.Unmarshal(after, &afterMap); err != nil {
		t.Fatalf("unmarshaling delete audit after: %v", err)
	}
	if afterMap["deleted_at"] == nil {
		t.Errorf("delete audit after.deleted_at is nil, want it set")
	}
}

func TestSessionRepository_Delete_StaleVersionReturnsVersionMismatch(t *testing.T) {
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
	created, err := repo.Create(ctx, creator, ev.ID, validSessionInput(rm.ID, speaker, "Stale delete "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Delete(ctx, creator, ev.ID, created.ID, created.Version+99)
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("got err %v, want domain.ErrVersionMismatch", err)
	}

	if _, err := repo.Get(ctx, ev.ID, created.ID); err != nil {
		t.Fatalf("Get after failed delete: got err %v, want the session to still exist", err)
	}
}

func TestSessionRepository_Delete_MissingRowReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	creator := createTestUser(t, pool)
	ev := createTestEvent(t, pool, creator)

	_, err := repo.Delete(context.Background(), creator, ev.ID, "01900000-0000-7000-8000-000000000000", 1)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got err %v, want domain.ErrNotFound", err)
	}
}

func TestSessionRepository_Delete_WrongEventReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := postgres.NewSessionRepository(pool)
	ctx := context.Background()

	creator := createTestUser(t, pool)
	speaker := createTestUser(t, pool)
	ev1 := createTestEvent(t, pool, creator)
	ev2 := createTestEvent(t, pool, creator)
	rm, err := postgres.NewRoomRepository(pool).Create(ctx, creator, ev1.ID, validRoomInput("Ev1 hall "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("creating test room: %v", err)
	}
	created, err := repo.Create(ctx, creator, ev1.ID, validSessionInput(rm.ID, speaker, "Ev1 session "+uniqueSubject(t)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.Delete(ctx, creator, ev2.ID, created.ID, created.Version)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete via wrong event: got err %v, want domain.ErrNotFound", err)
	}
	if _, err := repo.Get(ctx, ev1.ID, created.ID); err != nil {
		t.Fatalf("session should still exist under its real event: %v", err)
	}
}
