package response

import (
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// MemberResponse is every role's member shape -- item 09 shipped one flat
// struct before item 10 existed; item 10 (FEATURES.md, docs/requirements.md
// D11) makes UserEmail role-gated. Attendees have no member:read permission
// at all (docs/requirements.md §4), so they can never reach a route that
// builds one of these.
//
// UserEmail is a pointer so json's omitempty drops the "user_email" key
// entirely when nil, rather than emitting it with an empty string -- the
// key itself must not appear (CLAUDE.md: no email key anywhere), not just
// carry a blank value.
type MemberResponse struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	UserEmail *string   `json:"user_email,omitempty"`
	UserName  string    `json:"user_name"`
	Role      string    `json:"role"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// canSeeMemberEmail is an allowlist, deliberately -- D11 (docs/requirements.md
// §7.2): admin manages membership and needs email; contributor manages the
// schedule and needs names, not email. An allowlist fails safe when a
// fourth role is added later; a denylist (role != "attendee") would leak
// email to it by default.
func canSeeMemberEmail(callerRole string) bool {
	return callerRole == "admin"
}

// NewMemberResponse builds the presenter for callerRole -- the role Authz
// attached to the request context (middleware.RoleFromContext), resolved
// from the same event_members row policy.Can already looked up. That role
// is used here for response shaping ONLY, never to decide whether the
// request reaches this presenter at all -- role_permissions (via Authz)
// already made that call (FAILURES.md).
func NewMemberResponse(m member.Member, callerRole string) MemberResponse {
	resp := MemberResponse{
		ID:        m.ID,
		EventID:   m.EventID,
		UserID:    m.UserID,
		UserName:  m.UserName,
		Role:      m.Role,
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
	if canSeeMemberEmail(callerRole) {
		email := m.UserEmail
		resp.UserEmail = &email
	}
	return resp
}

// MemberListResponse is GET /v1/events/{eventId}/members' envelope.
// NextCursor is nil (omitted from the JSON) when there is no further page --
// never an offset.
type MemberListResponse struct {
	Data       []MemberResponse `json:"data"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

func NewMemberListResponse(page member.Page, callerRole string) MemberListResponse {
	data := make([]MemberResponse, len(page.Members))
	for i, m := range page.Members {
		data[i] = NewMemberResponse(m, callerRole)
	}

	resp := MemberListResponse{Data: data}
	if page.NextCursor != nil {
		token := utils.EncodeCursor(page.NextCursor.CreatedAt, page.NextCursor.ID)
		resp.NextCursor = &token
	}
	return resp
}
