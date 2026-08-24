// Package event is the event aggregate -- the container everything else
// (rooms, sessions, members, invitations) hangs off (docs/requirements.md
// §2). Timestamps are timestamptz; Timezone is the IANA name they render
// into on the way out. No framework imports -- pgx/goqu stay in
// adapter/postgres.
package event

import "time"

// Event is the row shape every layer above adapter/postgres works with.
// DeletedAt never appears here -- a soft-deleted row surfaces as
// domain.ErrNotFound, never as a zero-value or a visible field.
type Event struct {
	ID          string
	Name        string
	Description *string
	Timezone    string
	StartsAt    time.Time
	EndsAt      time.Time
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
