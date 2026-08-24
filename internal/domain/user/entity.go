// Package user is the user aggregate: a global identity created on first
// sign-in by mapping the OIDC sub claim to a row. Per-event roles live on
// event_members, never here -- see item 07.
package user

import "time"

// User is the row auth middleware attaches to the request context once a
// token's sub claim has been mapped to it. ID is the textual form of the
// uuidv7 Postgres generates -- no UUID library dependency needed for a type
// this repo only ever compares and serializes, never decomposes.
type User struct {
	ID        string
	Subject   string
	Email     string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Claims is the identity a validated token carries, before it's been
// mapped to a User row. Lives here, not in adapter/auth, so both
// adapter/auth (which produces it) and http/middleware (which consumes it
// via a local narrow interface) can depend on the same type without http
// ever importing adapter directly.
type Claims struct {
	Subject string
	Email   string
	Name    string
}
