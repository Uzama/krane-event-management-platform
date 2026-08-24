package response

import (
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// MemberResponse is every role's member shape for this feature -- attendees
// have no member:read permission at all (docs/requirements.md §4), so they
// can never reach this route; the contributor/email-visibility nuance
// (§7.2) is item 10's job (role-based presenters), not this one's, matching
// how item 08 shipped one flat EventResponse before item 10 existed.
type MemberResponse struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	UserEmail string    `json:"user_email"`
	UserName  string    `json:"user_name"`
	Role      string    `json:"role"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewMemberResponse(m member.Member) MemberResponse {
	return MemberResponse{
		ID:        m.ID,
		EventID:   m.EventID,
		UserID:    m.UserID,
		UserEmail: m.UserEmail,
		UserName:  m.UserName,
		Role:      m.Role,
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// MemberListResponse is GET /v1/events/{eventId}/members' envelope.
// NextCursor is nil (omitted from the JSON) when there is no further page --
// never an offset.
type MemberListResponse struct {
	Data       []MemberResponse `json:"data"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

func NewMemberListResponse(page member.Page) MemberListResponse {
	data := make([]MemberResponse, len(page.Members))
	for i, m := range page.Members {
		data[i] = NewMemberResponse(m)
	}

	resp := MemberListResponse{Data: data}
	if page.NextCursor != nil {
		token := utils.EncodeCursor(page.NextCursor.CreatedAt, page.NextCursor.ID)
		resp.NextCursor = &token
	}
	return resp
}
