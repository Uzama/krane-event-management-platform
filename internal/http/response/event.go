package response

import (
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// EventResponse is every role's event shape for this feature -- the event
// resource itself has no roster/email to hide, unlike member endpoints
// (item 10). DeletedAt is never a field here: a soft-deleted event surfaces
// as 404, not as a visible flag.
type EventResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Timezone    string    `json:"timezone"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func NewEventResponse(e event.Event) EventResponse {
	return EventResponse{
		ID:          e.ID,
		Name:        e.Name,
		Description: e.Description,
		Timezone:    e.Timezone,
		StartsAt:    e.StartsAt,
		EndsAt:      e.EndsAt,
		Version:     e.Version,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// EventListResponse is GET /v1/events' envelope. NextCursor is nil (omitted
// from the JSON) when there is no further page -- never an offset.
type EventListResponse struct {
	Data       []EventResponse `json:"data"`
	NextCursor *string         `json:"next_cursor,omitempty"`
}

func NewEventListResponse(page event.Page) EventListResponse {
	data := make([]EventResponse, len(page.Events))
	for i, e := range page.Events {
		data[i] = NewEventResponse(e)
	}

	resp := EventListResponse{Data: data}
	if page.NextCursor != nil {
		token := utils.EncodeCursor(page.NextCursor.CreatedAt, page.NextCursor.ID)
		resp.NextCursor = &token
	}
	return resp
}
