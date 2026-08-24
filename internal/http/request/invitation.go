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

// BulkInviteRequest is POST /v1/events/{eventId}/invitations/bulk's body
// (item 21). Each item is shape-validated exactly like a single
// InvitationCreateRequest -- the per-item escalation guard (who may invite
// at that role) still lives in the repository's atomic write, not here,
// matching CreateInvitation's own boundary/authorization split.
type BulkInviteRequest struct {
	Invitations []InvitationCreateRequest `json:"invitations"`
}

func (r BulkInviteRequest) Validate() map[string]any {
	issues := map[string]any{}

	if len(r.Invitations) == 0 {
		issues["invitations"] = "must contain at least one invitation"
		return issues
	}

	perItem := make([]map[string]any, 0, len(r.Invitations))
	anyIssues := false
	for _, item := range r.Invitations {
		itemIssues := item.Validate()
		if len(itemIssues) > 0 {
			anyIssues = true
		}
		perItem = append(perItem, itemIssues)
	}
	if anyIssues {
		issues["invitations"] = perItem
	}

	return issues
}

func (r BulkInviteRequest) ToCreateInputs() []invitation.CreateInput {
	items := make([]invitation.CreateInput, len(r.Invitations))
	for i, item := range r.Invitations {
		items[i] = item.ToCreateInput()
	}
	return items
}
