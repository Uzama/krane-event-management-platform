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

	// RoomName/SpeakerName are populated by Repository.List only (item 18)
	// -- a single JOIN against rooms/users keeps List's query count
	// constant regardless of result size, batching what would otherwise be
	// a per-row lookup. Get/Create/Update/Delete leave these "" since none
	// of them has an N+1 to avoid; the http/response presenter uses that
	// to decide whether to surface room_name/speaker_name at all.
	RoomName    string
	SpeakerName string

	// SeriesID is set when this session was materialized as one occurrence
	// of a recurring series (item 23) -- nil for a standalone session.
	SeriesID *string
}

// Series is a recurrence rule, materialized eagerly into ordinary Session
// rows at creation time -- kept for history/attribution only (D-scoped:
// lazy materialize-on-read was cut, TRADEOFFS.md). Every occurrence it
// produced already exists as a real Session, subject to the same
// EXCLUDE/version/audit as one created individually.
type Series struct {
	ID            string
	EventID       string
	RoomID        string
	SpeakerID     string
	Title         string
	Description   *string
	Freq          string // "daily" | "weekly"
	IntervalCount int
	Occurrences   int
	CreatedAt     time.Time
}

// SeriesCreateInput is everything needed to materialize a series.
// FirstStartsAt/FirstEndsAt are the first occurrence's instants; every
// later occurrence is offset by IntervalCount * (1 day or 7 days,
// depending on Freq), preserving the same duration.
type SeriesCreateInput struct {
	RoomID        string
	SpeakerID     string
	Title         string
	Description   *string
	FirstStartsAt time.Time
	FirstEndsAt   time.Time
	Freq          string
	IntervalCount int
	Occurrences   int
}

// SeriesOccurrenceResult is one occurrence's outcome from CreateSeries --
// the same "defined per-item result, never an accident" shape item 21's
// BulkItemResult established, since a series occurrence can just as
// validly conflict with an existing booking (item 16's EXCLUDE) as any
// other session create.
type SeriesOccurrenceResult struct {
	StartsAt  time.Time
	Status    string // "created" | "conflict"
	SessionID *string
}
