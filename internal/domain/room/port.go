package room

import (
	"context"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

// CreateInput is everything needed to create a room. Capacity is nullable
// on the wire and in Postgres (rooms_capacity_check allows NULL or > 0).
type CreateInput struct {
	Name     string
	Capacity *int
}

// Patch carries only the fields a PATCH request actually set. Capacity is
// opt.Optional[*int] so absent (don't touch), explicit null (clear), and an
// explicit value (set) stay three distinct outcomes -- the same pattern as
// event.Patch.Description.
type Patch struct {
	Name     opt.Optional[string]
	Capacity opt.Optional[*int]
}

// Cursor is the opaque keyset position a List page was cut at -- the last
// row's (created_at, id). Never an offset (CLAUDE.md: pagination is keyset
// only).
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Page is one page of a List call. NextCursor is nil when there is no
// further page.
type Page struct {
	Rooms      []Room
	NextCursor *Cursor
}

// Repository is implemented by adapter/postgres. Every write is one
// transaction: the row mutation and its audit_log row commit or roll back
// together, never a check-then-act SELECT beforehand.
type Repository interface {
	// Create inserts the room under eventID and audits it. Returns
	// ErrConflict if eventID already has a room with that name
	// (rooms_event_id_name_key).
	Create(ctx context.Context, actorID, eventID string, in CreateInput) (Room, error)

	// Get returns the room, scoped to both id and eventID -- a room that
	// exists but belongs to a different event returns ErrNotFound, the same
	// as a room that doesn't exist at all.
	Get(ctx context.Context, eventID, roomID string) (Room, error)

	// List returns eventID's rooms, ordered by (created_at, id), starting
	// after the given cursor.
	List(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error)

	// Update applies patch atomically, gated on version and scoped to
	// eventID. Zero rows affected disambiguates, via a diagnostic read after
	// the failed write, into ErrNotFound (missing or wrong event) or
	// ErrVersionMismatch (a concurrent write already changed it).
	// ErrConflict means the new name collides with another room in the same
	// event.
	Update(ctx context.Context, actorID, eventID, roomID string, version int, patch Patch) (Room, error)

	// Delete hard-deletes the room, scoped to eventID and gated on version
	// the same way Update is. ErrConflict means the room still has sessions
	// referencing it (sessions.room_id has no ON DELETE action) -- the
	// caller must remove or reassign them first.
	Delete(ctx context.Context, actorID, eventID, roomID string, version int) error
}
