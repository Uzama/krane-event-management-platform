// Package request holds request DTOs -- decoded from the wire, validated at
// this boundary (CLAUDE.md: "Only validate at system boundaries"), then
// converted to the domain shapes services consume. PATCH fields use
// domain/opt.Optional[T] so an absent field and an explicit null are never
// conflated.
package request

import (
	"strings"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

// CreateEventRequest is POST /v1/events' body. All fields are required --
// there is no absent-vs-null distinction to make on create.
type CreateEventRequest struct {
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Timezone    string    `json:"timezone"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
}

// Validate returns a field -> issue map for the 422 envelope's details.
// An empty map means the request is valid.
func (r CreateEventRequest) Validate() map[string]any {
	issues := map[string]any{}

	if strings.TrimSpace(r.Name) == "" {
		issues["name"] = "is required"
	}
	validateTimezone(r.Timezone, issues)
	if r.StartsAt.IsZero() {
		issues["starts_at"] = "is required"
	}
	if r.EndsAt.IsZero() {
		issues["ends_at"] = "is required"
	}
	if !r.StartsAt.IsZero() && !r.EndsAt.IsZero() && !r.EndsAt.After(r.StartsAt) {
		issues["ends_at"] = "must be after starts_at"
	}

	return issues
}

// ToCreateInput converts a validated request into the domain input. Callers
// must call Validate first -- ToCreateInput does not re-check anything.
func (r CreateEventRequest) ToCreateInput() event.CreateInput {
	return event.CreateInput{
		Name:        r.Name,
		Description: r.Description,
		Timezone:    r.Timezone,
		StartsAt:    r.StartsAt,
		EndsAt:      r.EndsAt,
	}
}

// PatchEventRequest is PATCH /v1/events/{eventId}'s body. Version is
// required (the optimistic-lock token the caller last read); every other
// field is an Optional so absent/null/value stay distinct. StartsAt and
// EndsAt must be set together -- validating the pair against a stored
// value would need a read the repository's single versioned UPDATE is
// built to avoid, so the request enforces the pair up front instead.
type PatchEventRequest struct {
	Version     int                     `json:"version"`
	Name        opt.Optional[string]    `json:"name"`
	Description opt.Optional[*string]   `json:"description"`
	Timezone    opt.Optional[string]    `json:"timezone"`
	StartsAt    opt.Optional[time.Time] `json:"starts_at"`
	EndsAt      opt.Optional[time.Time] `json:"ends_at"`
}

func (r PatchEventRequest) Validate() map[string]any {
	issues := map[string]any{}

	if r.Version <= 0 {
		issues["version"] = "is required"
	}
	if r.Name.Set && strings.TrimSpace(r.Name.Value) == "" {
		issues["name"] = "must not be blank"
	}
	if r.Timezone.Set {
		validateTimezone(r.Timezone.Value, issues)
	}
	if r.StartsAt.Set != r.EndsAt.Set {
		issues["starts_at/ends_at"] = "must both be provided together"
	} else if r.StartsAt.Set && r.EndsAt.Set && !r.EndsAt.Value.After(r.StartsAt.Value) {
		issues["ends_at"] = "must be after starts_at"
	}

	return issues
}

// ToPatch converts a validated request into the domain patch. Only fields
// with Set == true carry through -- an absent field never reaches the
// repository's dynamic UPDATE at all.
func (r PatchEventRequest) ToPatch() event.Patch {
	return event.Patch{
		Name:        r.Name,
		Description: r.Description,
		Timezone:    r.Timezone,
		StartsAt:    r.StartsAt,
		EndsAt:      r.EndsAt,
	}
}

func validateTimezone(tz string, issues map[string]any) {
	if strings.TrimSpace(tz) == "" {
		issues["timezone"] = "is required"
		return
	}
	if _, err := time.LoadLocation(tz); err != nil {
		issues["timezone"] = "must be a valid IANA timezone name"
	}
}
