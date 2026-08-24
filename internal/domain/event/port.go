package event

import (
	"context"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

// CreateInput is everything needed to create an event. Both StartsAt and
// EndsAt are required -- the ends-after-starts invariant is validated at
// the http/request boundary before this ever reaches the repository.
type CreateInput struct {
	Name        string
	Description *string
	Timezone    string
	StartsAt    time.Time
	EndsAt      time.Time
}

// Patch carries only the fields a PATCH request actually set. StartsAt and
// EndsAt are both-or-neither by the time they reach here -- http/request
// enforces that, since validating the pair against a stored value would
// need a read the repository's single versioned UPDATE is built to avoid.
type Patch struct {
	Name        opt.Optional[string]
	Description opt.Optional[*string]
	Timezone    opt.Optional[string]
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
	Events     []Event
	NextCursor *Cursor
}

// Repository is implemented by adapter/postgres. Create, Update, and Delete
// are each one transaction: the row mutation and its audit_log row commit
// or roll back together, never a check-then-act SELECT beforehand.
type Repository interface {
	// Create inserts the event, grants actorID an admin event_members row,
	// and audits both -- role_permissions has no event:create row, so this
	// is how the creator ever gains standing to read what they just made.
	Create(ctx context.Context, actorID string, in CreateInput) (Event, error)

	// Get returns the live (non-soft-deleted) event, or ErrNotFound.
	Get(ctx context.Context, id string) (Event, error)

	// List returns events userID has an event_members row for, ordered by
	// (created_at, id), starting after the given cursor.
	List(ctx context.Context, userID string, limit int, after *Cursor) (Page, error)

	// Update applies patch atomically, gated on version -- zero rows
	// affected returns ErrVersionMismatch (a concurrent write already
	// changed it) or ErrNotFound (deleted or missing), decided by a
	// diagnostic read after the failed write, never before it.
	Update(ctx context.Context, actorID, id string, version int, patch Patch) (Event, error)

	// Delete soft-deletes the event (sets deleted_at), gated on version the
	// same way Update is.
	Delete(ctx context.Context, actorID, id string, version int) (Event, error)
}
