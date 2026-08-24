// Package member is the event_members aggregate -- per-event roles
// (docs/requirements.md §4) and the roster item 09 (FEATURES.md) adds. No
// framework imports -- pgx/goqu stay in adapter/postgres.
package member

import "time"

// Member is the row shape every layer above adapter/postgres works with.
// UserEmail/UserName come from a join to users -- a roster of opaque ids
// is not a roster. Whether every role sees them is item 10's job (role-based
// presenters); this feature ships one flat shape, matching how
// domain/event.Event has no per-role variant either.
type Member struct {
	ID        string
	EventID   string
	UserID    string
	UserEmail string
	UserName  string
	Role      string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}
