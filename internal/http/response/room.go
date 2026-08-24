package response

import (
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// RoomResponse is every role's room shape -- like EventResponse, there is
// no email/roster to hide (item 10 only narrowed the member resource), so
// no callerRole parameter here.
type RoomResponse struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	Name      string    `json:"name"`
	Capacity  *int      `json:"capacity"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewRoomResponse(r room.Room) RoomResponse {
	return RoomResponse{
		ID:        r.ID,
		EventID:   r.EventID,
		Name:      r.Name,
		Capacity:  r.Capacity,
		Version:   r.Version,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// RoomListResponse is GET /v1/events/{eventId}/rooms' envelope. NextCursor
// is nil (omitted from the JSON) when there is no further page -- never an
// offset.
type RoomListResponse struct {
	Data       []RoomResponse `json:"data"`
	NextCursor *string        `json:"next_cursor,omitempty"`
}

func NewRoomListResponse(page room.Page) RoomListResponse {
	data := make([]RoomResponse, len(page.Rooms))
	for i, r := range page.Rooms {
		data[i] = NewRoomResponse(r)
	}

	resp := RoomListResponse{Data: data}
	if page.NextCursor != nil {
		token := utils.EncodeCursor(page.NextCursor.CreatedAt, page.NextCursor.ID)
		resp.NextCursor = &token
	}
	return resp
}
