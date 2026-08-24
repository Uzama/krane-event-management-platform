package session

import (
	"context"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

// CreateInput is everything needed to create a session. StartsAt/EndsAt are
// already-resolved instants -- http/request resolves the wire's local
// wall-clock strings against the event's timezone (via utils.ResolveLocalTime)
// before this ever reaches the service, so this package never handles a
// timezone name or a DST concern directly.
type CreateInput struct {
	RoomID      string
	SpeakerID   string
	Title       string
	Description *string
	StartsAt    time.Time
	EndsAt      time.Time

	// SeriesID tags this occurrence as belonging to a recurring series
	// (item 23) -- nil for an ordinary standalone session. Only
	// CreateSeries ever sets it; the single-session http request DTO never
	// populates this field, so a client cannot self-assign a session to an
	// arbitrary series through the regular create endpoint.
	SeriesID *string
}

// Patch carries only the fields a PATCH request actually set. RoomID and
// SpeakerID are deliberately not patchable -- a session's room and speaker
// are fixed at creation (docs/requirements.md §8, staged for TRADEOFFS.md);
// reassignment can be added later if needed. StartsAt and EndsAt are
// both-or-neither by the time they reach here, same as event.Patch.
type Patch struct {
	Title       opt.Optional[string]
	Description opt.Optional[*string]
	StartsAt    opt.Optional[time.Time]
	EndsAt      opt.Optional[time.Time]
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
	Sessions   []Session
	NextCursor *Cursor
}

// Repository is implemented by adapter/postgres. Every write is one
// transaction: the row mutation and its audit_log row commit or roll back
// together, never a check-then-act SELECT beforehand.
type Repository interface {
	// Create inserts the session under eventID and audits it. RoomID must
	// belong to eventID -- resolved atomically inside the same INSERT, not
	// a separate check; a room missing or belonging to a different event
	// returns ErrInvalidRoom, matching the existing precedent that a room
	// in the wrong event is indistinguishable from a missing one (item
	// 11's room.Get). SpeakerID must reference an existing user; a missing
	// one returns ErrInvalidSpeaker. When both are invalid, ErrInvalidRoom
	// always wins -- the room lookup gates the INSERT, so the speaker's
	// foreign key is never evaluated at all.
	Create(ctx context.Context, actorID, eventID string, in CreateInput) (Session, error)

	// Get returns the live (non-soft-deleted) session, scoped to both id
	// and eventID -- a session that exists but belongs to a different
	// event returns ErrNotFound, the same as a missing or soft-deleted one.
	Get(ctx context.Context, eventID, sessionID string) (Session, error)

	// List returns eventID's live sessions, ordered by (created_at, id),
	// starting after the given cursor.
	List(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error)

	// Update applies patch atomically, gated on version and scoped to
	// eventID, excluding soft-deleted rows. Zero rows affected
	// disambiguates, via a diagnostic read after the failed write, into
	// ErrNotFound (missing, wrong event, or soft-deleted) or
	// ErrVersionMismatch (a concurrent write already changed it).
	Update(ctx context.Context, actorID, eventID, sessionID string, version int, patch Patch) (Session, error)

	// Delete soft-deletes the session (sets deleted_at), gated on version
	// the same way Update is -- matching events, not rooms:
	// docs/requirements.md D9 says cancelling a session frees its room and
	// speaker slot immediately, which only makes sense against a
	// soft-delete plus item 16's later partial EXCLUDE constraints.
	Delete(ctx context.Context, actorID, eventID, sessionID string, version int) (Session, error)

	// CreateSeries materializes in.Occurrences sessions eagerly (item 23),
	// each through the exact same Create this repository already exposes
	// -- so each occurrence is independently subject to the room/speaker
	// EXCLUDE, version-gating, and audit every other session gets, never a
	// separate scheduling engine. A per-occurrence conflict (item 16) is a
	// defined per-item result, not a whole-series failure, matching item
	// 21's BulkCreate precedent.
	CreateSeries(ctx context.Context, actorID, eventID string, in SeriesCreateInput) (Series, []SeriesOccurrenceResult, error)
}
