package request

import (
	"strings"

	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
)

// InvitationCreateRequest is POST /v1/events/{eventId}/invitations' body.
// Boundary validation only checks shape (email present, role known) --
// whether actorID may invite at that role is an authorization decision,
// not a request-validation one, and lives in the repository's atomic
// write (roles.go's canGrantRoleGuard), answered as 403 cannot_invite_at_role,
// never 422.
type InvitationCreateRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (r InvitationCreateRequest) Validate() map[string]any {
	issues := map[string]any{}

	if strings.TrimSpace(r.Email) == "" {
		issues["email"] = "is required"
	}
	if !knownRoles[r.Role] {
		issues["role"] = "must be one of admin, contributor, attendee"
	}

	return issues
}

func (r InvitationCreateRequest) ToCreateInput() invitation.CreateInput {
	return invitation.CreateInput{Email: r.Email, Role: r.Role}
}
