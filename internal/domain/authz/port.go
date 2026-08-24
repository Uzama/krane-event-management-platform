// Package authz declares the authorization chokepoint's policy interface --
// the one thing every layer agrees to call before a mutation or a read of
// per-event data. The permission data and the membership lookup that
// answer it live in adapter/authz; enforcement lives in http/middleware.
package authz

import "context"

// Action is one of the actions role_permissions.action is CHECK-constrained
// to (migrations/20260823164421_init_schema.up.sql).
type Action string

const (
	ActionCreate     Action = "create"
	ActionRead       Action = "read"
	ActionUpdate     Action = "update"
	ActionDelete     Action = "delete"
	ActionAssignRole Action = "assign-role"
)

// Resource is one of the resources role_permissions.resource is
// CHECK-constrained to.
type Resource string

const (
	ResourceEvent      Resource = "event"
	ResourceMember     Resource = "member"
	ResourceRoom       Resource = "room"
	ResourceSession    Resource = "session"
	ResourceInvitation Resource = "invitation"
)

// Policy answers can(user, action, resource) for a specific event -- roles
// are per-event (CLAUDE.md), so every question is scoped to one eventID.
// Implemented by adapter/authz.Policy. A false, "", nil result means
// "denied"; it deliberately covers both "not a member of this event" and
// "this event does not exist" identically, so a caller with no standing to
// ask can never distinguish the two from the response.
//
// Can also returns the caller's role on eventID (the exact row it already
// looked up to answer allowed), "" if there is none. That role exists for
// ONE purpose: http/response presenters deciding what a body includes
// (item 10, FEATURES.md). It must never be used to gate whether a request
// proceeds -- allowed is the only chokepoint answer for that (FAILURES.md).
type Policy interface {
	Can(ctx context.Context, userID, eventID string, action Action, resource Resource) (allowed bool, role string, err error)
}
