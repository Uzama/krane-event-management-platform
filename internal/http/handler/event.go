package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
	"github.com/Uzama/krane-event-management-platform/internal/http/request"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

const (
	defaultListLimit = 25
	maxListLimit     = 100
)

// EventService is the narrowest seam EventHandler needs against
// domain/event.Service, matching the Pinger/TokenVerifier pattern -- http
// depends on this interface, never on the concrete service type.
type EventService interface {
	CreateEvent(ctx context.Context, actorID string, in event.CreateInput) (event.Event, error)
	GetEvent(ctx context.Context, id string) (event.Event, error)
	ListEvents(ctx context.Context, userID string, limit int, after *event.Cursor) (event.Page, error)
	UpdateEvent(ctx context.Context, actorID, id string, version int, patch event.Patch) (event.Event, error)
	DeleteEvent(ctx context.Context, actorID, id string, version int) (event.Event, error)
}

// EventHandler is thin: decode, validate, call the service, encode. Every
// invariant lives in the request DTO (validation) or the repository
// (atomicity, version-gating) -- not here.
type EventHandler struct {
	service EventService
	logger  *slog.Logger
}

func NewEventHandler(service EventService, logger *slog.Logger) *EventHandler {
	return &EventHandler{service: service, logger: logger}
}

// CreateEvent handles POST /v1/events. Not behind Authz -- role_permissions
// has no event:create row (item 07); any authenticated user may create an
// event, and the service grants them admin membership on it atomically.
func (h *EventHandler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("event: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	var req request.CreateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	created, err := h.service.CreateEvent(r.Context(), actor.ID, req.ToCreateInput())
	if err != nil {
		h.logger.Error("event: creating", "actor_id", actor.ID, "error", err)
		h.writeInternalError(w)
		return
	}

	h.writeJSON(w, http.StatusCreated, response.NewEventResponse(created))
}

// GetEvent handles GET /v1/events/{eventId}, mounted behind Authz(event,
// read).
func (h *EventHandler) GetEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("event: no eventId path value; route is not /v1/events/{eventId}")
		h.writeInternalError(w)
		return
	}

	got, err := h.service.GetEvent(r.Context(), eventID)
	if err != nil {
		h.writeDomainError(w, r.Context(), "event: getting", eventID, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewEventResponse(got))
}

// ListEvents handles GET /v1/events. Not behind Authz -- scoping is a query
// against event_members, not a per-resource Can() check (docs/requirements.md
// §4 point 3: access is membership).
func (h *EventHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("event: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	limit := defaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxListLimit {
			h.writeValidationError(w, map[string]any{"limit": "must be a positive integer no greater than " + strconv.Itoa(maxListLimit)})
			return
		}
		limit = n
	}

	var after *event.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		createdAt, id, err := utils.DecodeCursor(raw)
		if err != nil {
			if writeErr := response.WriteError(w, http.StatusBadRequest, "invalid_cursor", "the cursor query parameter is malformed", nil); writeErr != nil {
				h.logger.Error("event: writing error envelope", "error", writeErr)
			}
			return
		}
		after = &event.Cursor{CreatedAt: createdAt, ID: id}
	}

	page, err := h.service.ListEvents(r.Context(), actor.ID, limit, after)
	if err != nil {
		h.logger.Error("event: listing", "actor_id", actor.ID, "error", err)
		h.writeInternalError(w)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewEventListResponse(page))
}

// PatchEvent handles PATCH /v1/events/{eventId}, mounted behind Authz(event,
// update).
func (h *EventHandler) PatchEvent(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("event: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("event: no eventId path value; route is not /v1/events/{eventId}")
		h.writeInternalError(w)
		return
	}

	var req request.PatchEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	updated, err := h.service.UpdateEvent(r.Context(), actor.ID, eventID, req.Version, req.ToPatch())
	if err != nil {
		h.writeDomainError(w, r.Context(), "event: updating", eventID, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewEventResponse(updated))
}

// DeleteEvent handles DELETE /v1/events/{eventId}?version=N, mounted behind
// Authz(event, delete). version is a required query parameter. Item 17
// resolved the forward-note this comment used to carry -- query-param (here)
// / body (PatchEvent) version-gating is the permanent design, not an
// interim one pending an ETag/If-Match retrofit; it is already uniform
// across events/rooms/sessions/event_members and satisfies FEATURES.md's
// stated alternative ("...or version in body").
func (h *EventHandler) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("event: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("event: no eventId path value; route is not /v1/events/{eventId}")
		h.writeInternalError(w)
		return
	}

	raw := r.URL.Query().Get("version")
	version, err := strconv.Atoi(raw)
	if raw == "" || err != nil || version <= 0 {
		h.writeValidationError(w, map[string]any{"version": "is required and must be a positive integer"})
		return
	}

	if _, err := h.service.DeleteEvent(r.Context(), actor.ID, eventID, version); err != nil {
		h.writeDomainError(w, r.Context(), "event: deleting", eventID, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeDomainError maps UpdateEvent/DeleteEvent's shared error vocabulary.
// ErrVersionMismatch (item 17) embeds the event's current state under
// details.current -- CLAUDE.md: "0 rows affected -> 409 with the current
// state" -- so a caller can retry immediately with the fresh version
// instead of a second round trip. The re-fetch happens after the write has
// already failed, so it decides nothing the failed write depended on --
// not a check-then-act.
func (h *EventHandler) writeDomainError(w http.ResponseWriter, ctx context.Context, logMsg, eventID string, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if writeErr := response.WriteError(w, http.StatusNotFound, "not_found", "no such event", nil); writeErr != nil {
			h.logger.Error("event: writing error envelope", "error", writeErr)
		}
	case errors.Is(err, domain.ErrVersionMismatch):
		details := map[string]any{"current": nil}
		switch current, getErr := h.service.GetEvent(ctx, eventID); {
		case getErr == nil:
			details["current"] = response.NewEventResponse(current)
		case errors.Is(getErr, domain.ErrNotFound):
			// The winning writer deleted the event in the gap between the
			// failed write and this re-fetch -- expected under concurrent
			// writers, not a bug. details.current stays explicitly null.
		default:
			h.logger.Error("event: fetching current state after version conflict", "event_id", eventID, "error", getErr)
		}
		if writeErr := response.WriteError(w, http.StatusConflict, "version_conflict", "the event was modified by someone else; reload and retry", details); writeErr != nil {
			h.logger.Error("event: writing error envelope", "error", writeErr)
		}
	default:
		h.logger.Error(logMsg, "event_id", eventID, "error", err)
		h.writeInternalError(w)
	}
}

func (h *EventHandler) writeValidationError(w http.ResponseWriter, issues map[string]any) {
	if err := response.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "the request failed validation", issues); err != nil {
		h.logger.Error("event: writing error envelope", "error", err)
	}
}

func (h *EventHandler) writeInternalError(w http.ResponseWriter) {
	if err := response.WriteError(w, http.StatusInternalServerError, "internal", "an unexpected error occurred", nil); err != nil {
		h.logger.Error("event: writing error envelope", "error", err)
	}
}

func (h *EventHandler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("event: encoding response", "error", err)
	}
}
