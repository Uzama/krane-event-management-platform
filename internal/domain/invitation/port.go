package invitation

import (
	"context"
	"time"
)

// CreateInput is everything needed to invite someone by email -- there is
// no "search users" endpoint, and an invitation may name someone who has
// never signed in (D2).
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
	Invitations []Invitation
	NextCursor  *Cursor
}

// Repository is implemented by adapter/postgres. Every write is one
// transaction: the row mutation and its audit_log row commit or roll back
// together, never a check-then-act SELECT beforehand.
type Repository interface {
	// Create invites in.Email to eventID at in.Role, resolving an existing
	// user's id atomically in the same statement as the insert -- an
	// unmatched email is not an error (D2), unlike member add. The domain
	// policy this enforces: only an admin may invite at any role but
	// attendee; a contributor may invite attendee only. That guard runs
	// inside the same atomic write (no separate privilege check that could
	// race the insert) and fails closed -- an actorID with no membership row
	// on eventID at all is denied every invite, including attendee, not
	// just elevated roles. Returns ErrForbidden if actorID cannot invite at
	// that role, or ErrConflict if eventID already has an invitation for
	// that email (invitations_event_id_email_key) -- there is no update or
	// delete path, so a wrong-role invitation cannot be corrected via this
	// API; it must be re-sent under a different email or left as-is.
	Create(ctx context.Context, actorID, eventID string, in CreateInput) (Invitation, error)

	// List returns eventID's invitations, ordered by (created_at, id),
	// starting after the given cursor.
	List(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error)
}
