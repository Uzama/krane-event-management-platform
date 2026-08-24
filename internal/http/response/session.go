package response

import (
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// SessionResponse is every role's session shape -- like EventResponse and
// RoomResponse, there is no email/roster to hide, so no callerRole
// parameter. StartsAt/EndsAt are localized to the event's own IANA
// timezone (never left in UTC): "rendered into timezone on the way out",
// docs/requirements.md's events table already names this convention.
type SessionResponse struct {
	ID              string    `json:"id"`
	EventID         string    `json:"event_id"`
	RoomID          string    `json:"room_id"`
	SpeakerID       string    `json:"speaker_id"`
	Title           string    `json:"title"`
	Description     *string   `json:"description"`
	StartsAt        time.Time `json:"starts_at"`
	EndsAt          time.Time `json:"ends_at"`
	DurationMinutes int       `json:"duration_minutes"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// NewSessionResponse takes an already-loaded *time.Location, never a
// timezone string to re-parse -- the caller (ListSessions especially)
// loads it once per request, not once per row. StartsAt and EndsAt are
// localized INDEPENDENTLY via In(loc), so a session spanning a DST
// transition correctly carries two different offsets; DurationMinutes is
// computed from the raw instants BEFORE localizing (s.EndsAt.Sub(s.StartsAt)),
// never from the localized wall-clock fields -- component subtraction
// across a transition gives the wrong answer, which is exactly the bug
// FEATURES.md item 12's DST must-test exists to catch.
func NewSessionResponse(s session.Session, loc *time.Location) SessionResponse {
	return SessionResponse{
		ID:              s.ID,
		EventID:         s.EventID,
		RoomID:          s.RoomID,
		SpeakerID:       s.SpeakerID,
		Title:           s.Title,
		Description:     s.Description,
		StartsAt:        s.StartsAt.In(loc),
		EndsAt:          s.EndsAt.In(loc),
		DurationMinutes: int(s.EndsAt.Sub(s.StartsAt).Minutes()),
		Version:         s.Version,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
	}
}

// SessionListResponse is GET /v1/events/{eventId}/sessions' envelope.
// NextCursor is nil (omitted from the JSON) when there is no further page
// -- never an offset.
type SessionListResponse struct {
	Data       []SessionResponse `json:"data"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

// NewSessionListResponse takes the same already-loaded *time.Location as
// NewSessionResponse and reuses it across every row in the page -- the
// event's timezone is constant across a page, so time.LoadLocation runs
// once per request, not once per row.
func NewSessionListResponse(page session.Page, loc *time.Location) SessionListResponse {
	data := make([]SessionResponse, len(page.Sessions))
	for i, s := range page.Sessions {
		data[i] = NewSessionResponse(s, loc)
	}

	resp := SessionListResponse{Data: data}
	if page.NextCursor != nil {
		token := utils.EncodeCursor(page.NextCursor.CreatedAt, page.NextCursor.ID)
		resp.NextCursor = &token
	}
	return resp
}
