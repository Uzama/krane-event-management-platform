// Package invitation is the invitations aggregate -- an independent record
// of who was invited to an event, not a pre-membership state machine
// (docs/requirements.md D1). No framework imports -- pgx/goqu stay in
// adapter/postgres.
package invitation

import "time"

// Invitation is the row shape every layer above adapter/postgres works
// with. UserID is nullable and resolved once, atomically, at Create time
// (D2: an invitation whose recipient must already have an account is not
// an invitation) -- it is never re-resolved later if the invitee signs up
// afterward. A future "my invitations" endpoint must therefore match on
// Email, not UserID, to find invitations sent before the invitee had an
// account.
type Invitation struct {
	ID        string
	EventID   string
	UserID    *string
	Email     string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}
