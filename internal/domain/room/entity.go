// Package room is the room aggregate -- a place a session happens, scoped
// to exactly one event (docs/requirements.md D8: cross-event physical-room
// conflicts are out of scope). No framework imports -- pgx/goqu stay in
// adapter/postgres.
package room

import "time"

// Room is the row shape every layer above adapter/postgres works with.
// Rooms have no deleted_at -- unlike events/sessions, deletion here is a
// real hard DELETE (migrations/20260823164421_init_schema.up.sql).
type Room struct {
	ID        string
	EventID   string
	Name      string
	Capacity  *int
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}
