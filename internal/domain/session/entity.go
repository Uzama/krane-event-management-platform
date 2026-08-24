// Package session is the session aggregate -- a scheduled item inside an
// event, in a room, with a speaker. Timestamps are timestamptz instants;
// resolving/localizing them against the event's IANA timezone happens at
// the http boundary (item 12), not here. No framework imports -- pgx/goqu
// stay in adapter/postgres.
package session

import "time"

// Session is the row shape every layer above adapter/postgres works with.
// DeletedAt never appears here -- a soft-deleted row surfaces as
// domain.ErrNotFound, never as a zero-value or a visible field, matching
// event.Event's precedent.
type Session struct {
	ID          string
	EventID     string
	RoomID      string
	SpeakerID   string
	Title       string
	Description *string
	StartsAt    time.Time
	EndsAt      time.Time
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
