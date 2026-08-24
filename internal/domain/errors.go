// Package domain holds the sentinel errors shared across every aggregate --
// the one vocabulary adapter/postgres translates a constraint violation
// into and http maps to a status code (CLAUDE.md: "domain declares
// ErrConflict; adapter/postgres detects the violation and returns that
// domain error; http maps it to 409. No layer reaches around another.").
package domain

import "errors"

// ErrConflict is a business-rule conflict a database constraint enforces --
// e.g. a session's EXCLUDE overlap (item 16). Maps to 409.
var ErrConflict = errors.New("domain: conflict")

// ErrVersionMismatch is the optimistic-lock failure: an update or delete
// targeted a row whose version no longer matches, because someone else
// wrote it first. Maps to 409, never last-write-wins.
var ErrVersionMismatch = errors.New("domain: version mismatch")

// ErrForbidden is a domain-level authorization failure, distinct from the
// http/middleware Authz chokepoint's own 403 -- for the rare case a service
// must refuse an action for reasons role_permissions can't express. Maps
// to 403.
var ErrForbidden = errors.New("domain: forbidden")

// ErrNotFound means the row does not exist, or exists but is soft-deleted
// -- a caller who is already a member of the containing event (so the authz
// chokepoint let them through) attempted to reach a resource that is gone.
// Maps to 404.
var ErrNotFound = errors.New("domain: not found")
