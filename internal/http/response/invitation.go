package response

import (
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// InvitationResponse is every role's invitation shape. Unlike
// MemberResponse, there is no role-gated field: email is visible to both
// admin and contributor (a documented, scoped exception to D11 --
// docs/requirements.md D14 -- since the email is data the caller supplied
// to create this record, not roster PII someone else added). Attendee has
// no invitation:read permission at all, so no role ever reaches this
// presenter needing it hidden.
type InvitationResponse struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    *string   `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewInvitationResponse(i invitation.Invitation) InvitationResponse {
	return InvitationResponse{
		ID:        i.ID,
		EventID:   i.EventID,
		UserID:    i.UserID,
		Email:     i.Email,
		Role:      i.Role,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}
}

// InvitationListResponse is GET /v1/events/{eventId}/invitations' envelope.
// NextCursor is nil (omitted from the JSON) when there is no further page --
// never an offset.
type InvitationListResponse struct {
	Data       []InvitationResponse `json:"data"`
	NextCursor *string              `json:"next_cursor,omitempty"`
}

func NewInvitationListResponse(page invitation.Page) InvitationListResponse {
	data := make([]InvitationResponse, len(page.Invitations))
	for i, inv := range page.Invitations {
		data[i] = NewInvitationResponse(inv)
	}

	resp := InvitationListResponse{Data: data}
	if page.NextCursor != nil {
		token := utils.EncodeCursor(page.NextCursor.CreatedAt, page.NextCursor.ID)
		resp.NextCursor = &token
	}
	return resp
}
