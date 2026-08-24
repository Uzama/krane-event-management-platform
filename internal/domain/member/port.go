package member

import (
	"context"
	"time"
)

// CreateInput identifies the user to add by email (there is no "search
// users" endpoint, and event_members.user_id is a required FK to an
// existing users row) and the role to grant them.
type CreateInput struct {
	Email string
	Role  string
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
	Members    []Member
	NextCursor *Cursor
}

// Repository is implemented by adapter/postgres. Every write is one
// transaction: the row mutation and its audit_log row commit or roll back
// together, never a check-then-act SELECT beforehand.
type Repository interface {
	// Create resolves email to an existing user and grants them role on
	// eventID, atomically -- the same statement both resolves the email and
	// enforces that actorID is allowed to grant that role (only admin may
	// grant anything but attendee), so there is no separate privilege check
	// that could race the insert. Returns ErrNotFound if no user has that
	// email, ErrForbidden if actorID cannot grant the requested role, or
	// ErrConflict if actorID is already a member of eventID.
	Create(ctx context.Context, actorID, eventID string, in CreateInput) (Member, error)

	// Get returns a single member, scoped to both id and eventID. Returns
	// ErrNotFound if no such membership exists in that event.
	Get(ctx context.Context, eventID, memberID string) (Member, error)

	// List returns eventID's roster, ordered by (created_at, id), starting
	// after the given cursor.
	List(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error)

	// AssignRole changes memberID's role, gated on version. Zero rows
	// affected disambiguates, via a diagnostic read after the failed write,
	// into ErrNotFound (missing), ErrVersionMismatch (a concurrent write
	// already changed it), or ErrConflict (this is eventID's last admin and
	// the new role is not admin -- last-admin protection, item 09).
	AssignRole(ctx context.Context, actorID, eventID, memberID string, version int, role string) (Member, error)

	// Delete removes memberID from eventID, gated on version the same way
	// AssignRole is, with the identical last-admin ErrConflict case. A real
	// hard DELETE -- event_members has no deleted_at column.
	Delete(ctx context.Context, actorID, eventID, memberID string, version int) error
}
