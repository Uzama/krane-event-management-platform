package request

import (
	"strings"

	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
)

// knownRoles mirrors event_members' CHECK constraint (migrations/
// 20260823164421_init_schema.up.sql) -- rejecting an unknown role here means
// Postgres never has to.
var knownRoles = map[string]bool{"admin": true, "contributor": true, "attendee": true}

// AddMemberRequest is POST /v1/events/{eventId}/members' body. Identifies
// the target by email -- there is no "search users" endpoint, and
// event_members.user_id is a required FK to an existing users row.
type AddMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (r AddMemberRequest) Validate() map[string]any {
	issues := map[string]any{}

	if strings.TrimSpace(r.Email) == "" {
		issues["email"] = "is required"
	}
	if !knownRoles[r.Role] {
		issues["role"] = "must be one of admin, contributor, attendee"
	}

	return issues
}

func (r AddMemberRequest) ToCreateInput() member.CreateInput {
	return member.CreateInput{Email: r.Email, Role: r.Role}
}

// AssignRoleRequest is PATCH /v1/events/{eventId}/members/{memberId}'s body.
// Role is always the field being set -- there is no absent-vs-null
// distinction to make on a single-field assign, so no domain/opt.Optional[T]
// wrapper is needed here.
type AssignRoleRequest struct {
	Role    string `json:"role"`
	Version int    `json:"version"`
}

func (r AssignRoleRequest) Validate() map[string]any {
	issues := map[string]any{}

	if !knownRoles[r.Role] {
		issues["role"] = "must be one of admin, contributor, attendee"
	}
	if r.Version <= 0 {
		issues["version"] = "is required"
	}

	return issues
}
