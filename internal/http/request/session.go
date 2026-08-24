package request

import (
	"errors"
	"strings"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// CreateSessionRequest is POST /v1/events/{eventId}/sessions' body.
// StartsAt/EndsAt are wire-format strings, not time.Time -- encoding/json's
// default time.Time parsing expects an RFC3339 offset, and this endpoint
// deliberately rejects one: the server resolves the local wall-clock string
// against the event's own IANA timezone (FEATURES.md item 12), the caller
// never supplies an offset. RoomID/SpeakerID are fixed at creation --
// there is no PATCH path for them (docs/requirements.md §8).
type CreateSessionRequest struct {
	RoomID      string  `json:"room_id"`
	SpeakerID   string  `json:"speaker_id"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	StartsAt    string  `json:"starts_at"`
	EndsAt      string  `json:"ends_at"`
}

// Validate returns a field -> issue map for the 422 envelope's details.
// loc is the owning event's already-loaded IANA *time.Location (fetched by
// the handler once per request, before this is called) -- resolving
// starts_at/ends_at against it is what this validation step exists to do;
// it cannot happen without it. An empty map means the request is valid.
func (r CreateSessionRequest) Validate(loc *time.Location) map[string]any {
	issues := map[string]any{}

	if strings.TrimSpace(r.Title) == "" {
		issues["title"] = "is required"
	}
	if strings.TrimSpace(r.RoomID) == "" {
		issues["room_id"] = "is required"
	}
	if strings.TrimSpace(r.SpeakerID) == "" {
		issues["speaker_id"] = "is required"
	}

	startsAt, startsErr := utils.ResolveLocalTime(r.StartsAt, loc)
	if startsErr != nil {
		issues["starts_at"] = resolveTimeIssue(startsErr)
	}
	endsAt, endsErr := utils.ResolveLocalTime(r.EndsAt, loc)
	if endsErr != nil {
		issues["ends_at"] = resolveTimeIssue(endsErr)
	}
	if startsErr == nil && endsErr == nil && !endsAt.After(startsAt) {
		issues["ends_at"] = "must be after starts_at"
	}

	return issues
}

// ToCreateInput converts a validated request into the domain input. Callers
// must call Validate(loc) with the same *time.Location first -- ToCreateInput
// re-resolves starts_at/ends_at (cheap, deterministic, no I/O -- loc is
// already loaded) trusting they are already known-valid, the same
// "Validate first" contract CreateEventRequest.ToCreateInput documents.
func (r CreateSessionRequest) ToCreateInput(loc *time.Location) session.CreateInput {
	startsAt, _ := utils.ResolveLocalTime(r.StartsAt, loc)
	endsAt, _ := utils.ResolveLocalTime(r.EndsAt, loc)
	return session.CreateInput{
		RoomID:      r.RoomID,
		SpeakerID:   r.SpeakerID,
		Title:       r.Title,
		Description: r.Description,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
	}
}

// PatchSessionRequest is PATCH /v1/events/{eventId}/sessions/{sessionId}'s
// body. Version is required. RoomID/SpeakerID have no fields here at all --
// not merely omitted from required, genuinely unpatchable
// (docs/requirements.md §8). StartsAt/EndsAt must be set together, same as
// PatchEventRequest -- time_range is one column, so a partial change would
// need a read this repository's single versioned UPDATE is built to avoid.
type PatchSessionRequest struct {
	Version     int                   `json:"version"`
	Title       opt.Optional[string]  `json:"title"`
	Description opt.Optional[*string] `json:"description"`
	StartsAt    opt.Optional[string]  `json:"starts_at"`
	EndsAt      opt.Optional[string]  `json:"ends_at"`
}

func (r PatchSessionRequest) Validate(loc *time.Location) map[string]any {
	issues := map[string]any{}

	if r.Version <= 0 {
		issues["version"] = "is required"
	}
	if r.Title.Set && strings.TrimSpace(r.Title.Value) == "" {
		issues["title"] = "must not be blank"
	}

	if r.StartsAt.Set != r.EndsAt.Set {
		issues["starts_at/ends_at"] = "must both be provided together"
	} else if r.StartsAt.Set && r.EndsAt.Set {
		startsAt, startsErr := utils.ResolveLocalTime(r.StartsAt.Value, loc)
		if startsErr != nil {
			issues["starts_at"] = resolveTimeIssue(startsErr)
		}
		endsAt, endsErr := utils.ResolveLocalTime(r.EndsAt.Value, loc)
		if endsErr != nil {
			issues["ends_at"] = resolveTimeIssue(endsErr)
		}
		if startsErr == nil && endsErr == nil && !endsAt.After(startsAt) {
			issues["ends_at"] = "must be after starts_at"
		}
	}

	return issues
}

// ToPatch converts a validated request into the domain patch. Callers must
// call Validate(loc) with the same *time.Location first -- see
// CreateSessionRequest.ToCreateInput's doc comment.
func (r PatchSessionRequest) ToPatch(loc *time.Location) session.Patch {
	patch := session.Patch{
		Title:       r.Title,
		Description: r.Description,
	}
	if r.StartsAt.Set && r.EndsAt.Set {
		startsAt, _ := utils.ResolveLocalTime(r.StartsAt.Value, loc)
		endsAt, _ := utils.ResolveLocalTime(r.EndsAt.Value, loc)
		patch.StartsAt = opt.Of(startsAt)
		patch.EndsAt = opt.Of(endsAt)
	}
	return patch
}

// resolveTimeIssue turns a utils.ResolveLocalTime failure into the 422
// issue message for a starts_at/ends_at field -- a spring-forward gap gets
// its own explanation, distinct from an ordinary malformed-input error.
func resolveTimeIssue(err error) string {
	if errors.Is(err, utils.ErrNonexistentLocalTime) {
		return "does not exist in this event's timezone due to a DST transition"
	}
	return "must be a valid local date-time (" + utils.LocalTimeLayout + "), with no timezone offset"
}
