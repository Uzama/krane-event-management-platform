package request

import (
	"strings"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
)

// CreateRoomRequest is POST /v1/events/{eventId}/rooms' body. Capacity is a
// plain *int -- there is no absent-vs-null distinction to make on create.
type CreateRoomRequest struct {
	Name     string `json:"name"`
	Capacity *int   `json:"capacity"`
}

func (r CreateRoomRequest) Validate() map[string]any {
	issues := map[string]any{}

	if strings.TrimSpace(r.Name) == "" {
		issues["name"] = "is required"
	}
	if r.Capacity != nil && *r.Capacity <= 0 {
		issues["capacity"] = "must be greater than zero"
	}

	return issues
}

func (r CreateRoomRequest) ToCreateInput() room.CreateInput {
	return room.CreateInput{
		Name:     r.Name,
		Capacity: r.Capacity,
	}
}

// PatchRoomRequest is PATCH /v1/events/{eventId}/rooms/{roomId}'s body.
// Version is required; Capacity is opt.Optional[*int] so absent (don't
// touch), explicit null (clear), and an explicit value (set) stay three
// distinct outcomes -- the same pattern as PatchEventRequest.Description.
// A set value of exactly 0 is rejected: it is a *set* to an invalid value
// (rooms_capacity_check requires > 0), not a clear, and must not be
// conflated with the null-clears case.
type PatchRoomRequest struct {
	Version  int                  `json:"version"`
	Name     opt.Optional[string] `json:"name"`
	Capacity opt.Optional[*int]   `json:"capacity"`
}

func (r PatchRoomRequest) Validate() map[string]any {
	issues := map[string]any{}

	if r.Version <= 0 {
		issues["version"] = "is required"
	}
	if r.Name.Set && strings.TrimSpace(r.Name.Value) == "" {
		issues["name"] = "must not be blank"
	}
	if r.Capacity.Set && r.Capacity.Value != nil && *r.Capacity.Value <= 0 {
		issues["capacity"] = "must be greater than zero"
	}

	return issues
}

func (r PatchRoomRequest) ToPatch() room.Patch {
	return room.Patch{
		Name:     r.Name,
		Capacity: r.Capacity,
	}
}
