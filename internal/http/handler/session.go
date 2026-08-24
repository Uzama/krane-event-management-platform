package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
	"github.com/Uzama/krane-event-management-platform/internal/http/request"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// EventGetter is the narrowest seam SessionHandler needs against
// domain/event.Service -- every session operation needs the owning event's
// Timezone to resolve/localize local wall-clock times, and this is not a
// check-then-act: the timezone isn't a race-sensitive field, it's read-only
// input to a calculation. Named distinctly from EventService (event.go) --
// that interface's full CRUD surface is more than this handler needs.
type EventGetter interface {
	GetEvent(ctx context.Context, id string) (event.Event, error)
}

// SessionService is the narrowest seam SessionHandler needs against
// domain/session.Service, matching RoomService's pattern -- http depends on
// this interface, never on the concrete service type.
type SessionService interface {
	CreateSession(ctx context.Context, actorID, eventID string, in session.CreateInput) (session.Session, error)
	GetSession(ctx context.Context, eventID, sessionID string) (session.Session, error)
	ListSessions(ctx context.Context, eventID string, limit int, after *session.Cursor) (session.Page, error)
	UpdateSession(ctx context.Context, actorID, eventID, sessionID string, version int, patch session.Patch) (session.Session, error)
	DeleteSession(ctx context.Context, actorID, eventID, sessionID string, version int) (session.Session, error)
	CreateSeries(ctx context.Context, actorID, eventID string, in session.SeriesCreateInput) (session.Series, []session.SeriesOccurrenceResult, error)
}

// SessionHandler is thin: fetch the owning event (for its Timezone),
// decode, validate/resolve, call the service, encode. Every invariant
// lives in the request DTO (validation, DST resolution) or the repository
// (referential integrity, version-gating) -- not here.
type SessionHandler struct {
	service SessionService
	events  EventGetter
	logger  *slog.Logger
}

func NewSessionHandler(service SessionService, events EventGetter, logger *slog.Logger) *SessionHandler {
	return &SessionHandler{service: service, events: events, logger: logger}
}

// eventTimezone fetches eventID's event and loads its IANA zone once --
// callers (every method below) pass the same *time.Location into both
// request resolution and response localization, so ListSessions never
// reloads it per row. A LoadLocation failure here is defensive: the
// timezone was already validated as a real IANA name when the event was
// created (http/request.validateTimezone), so this should be unreachable
// in practice, but it is never silently ignored.
func (h *SessionHandler) eventTimezone(ctx context.Context, eventID string) (event.Event, *time.Location, error) {
	ev, err := h.events.GetEvent(ctx, eventID)
	if err != nil {
		return event.Event{}, nil, err
	}
	loc, err := time.LoadLocation(ev.Timezone)
	if err != nil {
		return event.Event{}, nil, err
	}
	return ev, loc, nil
}

// CreateSession handles POST /v1/events/{eventId}/sessions, mounted behind
// Authz(session, create).
func (h *SessionHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("session: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("session: no eventId path value; route is not /v1/events/{eventId}/sessions")
		h.writeInternalError(w)
		return
	}

	_, loc, err := h.eventTimezone(r.Context(), eventID)
	if err != nil {
		h.writeEventLookupError(w, "session: creating", eventID, err)
		return
	}

	var req request.CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(loc); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	created, err := h.service.CreateSession(r.Context(), actor.ID, eventID, req.ToCreateInput(loc))
	if err != nil {
		h.writeCreateOrUpdateError(w, "session: creating", eventID, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, response.NewSessionResponse(created, loc))
}

// CreateSeries handles POST /v1/events/{eventId}/sessions/series, mounted
// behind Authz(session, create) -- the same permission a plain session
// create uses, since every occurrence goes through the exact same write.
func (h *SessionHandler) CreateSeries(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("session: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("session: no eventId path value; route is not /v1/events/{eventId}/sessions/series")
		h.writeInternalError(w)
		return
	}

	_, loc, err := h.eventTimezone(r.Context(), eventID)
	if err != nil {
		h.writeEventLookupError(w, "session: creating series", eventID, err)
		return
	}

	var req request.SeriesCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(loc); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	series, results, err := h.service.CreateSeries(r.Context(), actor.ID, eventID, req.ToSeriesCreateInput(loc))
	if err != nil {
		h.writeCreateOrUpdateError(w, "session: creating series", eventID, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, response.NewSeriesResponse(series, results, loc))
}

// GetSession handles GET /v1/events/{eventId}/sessions/{sessionId},
// mounted behind Authz(session, read).
func (h *SessionHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	sessionID := r.PathValue("sessionId")
	if eventID == "" || sessionID == "" {
		h.logger.Error("session: missing eventId or sessionId path value; route is not /v1/events/{eventId}/sessions/{sessionId}")
		h.writeInternalError(w)
		return
	}

	_, loc, err := h.eventTimezone(r.Context(), eventID)
	if err != nil {
		h.writeEventLookupError(w, "session: getting", eventID, err)
		return
	}

	got, err := h.service.GetSession(r.Context(), eventID, sessionID)
	if err != nil {
		h.writeNotFoundOrInternal(w, "session: getting", eventID, sessionID, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewSessionResponse(got, loc))
}

// ListSessions handles GET /v1/events/{eventId}/sessions, mounted behind
// Authz(session, read). The event's *time.Location is loaded once here and
// threaded through every row response.NewSessionListResponse builds --
// never reloaded per row.
func (h *SessionHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("session: no eventId path value; route is not /v1/events/{eventId}/sessions")
		h.writeInternalError(w)
		return
	}

	_, loc, err := h.eventTimezone(r.Context(), eventID)
	if err != nil {
		h.writeEventLookupError(w, "session: listing", eventID, err)
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

	var after *session.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		createdAt, id, err := utils.DecodeCursor(raw)
		if err != nil {
			if writeErr := response.WriteError(w, http.StatusBadRequest, "invalid_cursor", "the cursor query parameter is malformed", nil); writeErr != nil {
				h.logger.Error("session: writing error envelope", "error", writeErr)
			}
			return
		}
		after = &session.Cursor{CreatedAt: createdAt, ID: id}
	}

	page, err := h.service.ListSessions(r.Context(), eventID, limit, after)
	if err != nil {
		h.logger.Error("session: listing", "event_id", eventID, "error", err)
		h.writeInternalError(w)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewSessionListResponse(page, loc))
}

// PatchSession handles PATCH /v1/events/{eventId}/sessions/{sessionId},
// mounted behind Authz(session, update). room_id/speaker_id are not
// patchable -- request.PatchSessionRequest has no fields for them
// (docs/requirements.md §8).
func (h *SessionHandler) PatchSession(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("session: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	sessionID := r.PathValue("sessionId")
	if eventID == "" || sessionID == "" {
		h.logger.Error("session: missing eventId or sessionId path value; route is not /v1/events/{eventId}/sessions/{sessionId}")
		h.writeInternalError(w)
		return
	}

	_, loc, err := h.eventTimezone(r.Context(), eventID)
	if err != nil {
		h.writeEventLookupError(w, "session: updating", eventID, err)
		return
	}

	var req request.PatchSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(loc); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	updated, err := h.service.UpdateSession(r.Context(), actor.ID, eventID, sessionID, req.Version, req.ToPatch(loc))
	if err != nil {
		h.writeUpdateOrDeleteError(w, r.Context(), loc, "session: updating", eventID, sessionID, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewSessionResponse(updated, loc))
}

// DeleteSession handles DELETE
// /v1/events/{eventId}/sessions/{sessionId}?version=N, mounted behind
// Authz(session, delete). version is a required query parameter -- item 17
// kept this design permanently rather than retrofitting ETag/If-Match; see
// event.go's DeleteEvent comment. This is a soft delete
// (session.Repository.Delete), matching events, not rooms.
func (h *SessionHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("session: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	sessionID := r.PathValue("sessionId")
	if eventID == "" || sessionID == "" {
		h.logger.Error("session: missing eventId or sessionId path value; route is not /v1/events/{eventId}/sessions/{sessionId}")
		h.writeInternalError(w)
		return
	}

	_, loc, err := h.eventTimezone(r.Context(), eventID)
	if err != nil {
		h.writeEventLookupError(w, "session: deleting", eventID, err)
		return
	}

	raw := r.URL.Query().Get("version")
	version, err := strconv.Atoi(raw)
	if raw == "" || err != nil || version <= 0 {
		h.writeValidationError(w, map[string]any{"version": "is required and must be a positive integer"})
		return
	}

	if _, err := h.service.DeleteSession(r.Context(), actor.ID, eventID, sessionID, version); err != nil {
		h.writeUpdateOrDeleteError(w, r.Context(), loc, "session: deleting", eventID, sessionID, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeEventLookupError maps eventTimezone's error vocabulary -- ErrNotFound
// (missing or soft-deleted event) is the only expected case; anything else
// (including a defensive LoadLocation failure) is a genuine internal error.
func (h *SessionHandler) writeEventLookupError(w http.ResponseWriter, logMsg, eventID string, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "not_found", "no such event")
		return
	}
	h.logger.Error(logMsg, "event_id", eventID, "error", err)
	h.writeInternalError(w)
}

// writeCreateOrUpdateError maps CreateSession's error vocabulary:
// session.ErrInvalidRoom/ErrInvalidSpeaker are create-only. ErrConflict
// (item 16) means the room or speaker is already booked for an overlapping
// time_range -- sessions_room_no_overlap_excl / sessions_speaker_no_overlap_excl.
func (h *SessionHandler) writeCreateOrUpdateError(w http.ResponseWriter, logMsg, eventID string, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidRoom):
		h.writeError(w, http.StatusNotFound, "room_not_found", "room_id must reference an existing room within this event")
	case errors.Is(err, session.ErrInvalidSpeaker):
		h.writeError(w, http.StatusNotFound, "speaker_not_found", "speaker_id must reference an existing user")
	case errors.Is(err, domain.ErrConflict):
		h.writeError(w, http.StatusConflict, "session_conflict", "that room or speaker is already booked for an overlapping time")
	default:
		h.logger.Error(logMsg, "event_id", eventID, "error", err)
		h.writeInternalError(w)
	}
}

// writeNotFoundOrInternal handles GetSession's error vocabulary -- Get only
// ever returns ErrNotFound or a genuine failure.
func (h *SessionHandler) writeNotFoundOrInternal(w http.ResponseWriter, logMsg, eventID, sessionID string, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	h.logger.Error(logMsg, "event_id", eventID, "session_id", sessionID, "error", err)
	h.writeInternalError(w)
}

// writeUpdateOrDeleteError maps PatchSession/DeleteSession's shared
// vocabulary. ErrVersionMismatch (item 17) embeds the session's current
// state under details.current, same as event.go/room.go's equivalent.
func (h *SessionHandler) writeUpdateOrDeleteError(w http.ResponseWriter, ctx context.Context, loc *time.Location, logMsg, eventID, sessionID string, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "no such session")
	case errors.Is(err, domain.ErrVersionMismatch):
		details := map[string]any{"current": nil}
		switch current, getErr := h.service.GetSession(ctx, eventID, sessionID); {
		case getErr == nil:
			details["current"] = response.NewSessionResponse(current, loc)
		case errors.Is(getErr, domain.ErrNotFound):
			// Winning writer deleted the session between the failed write
			// and this re-fetch -- expected, not a bug. Stays explicitly null.
		default:
			h.logger.Error("session: fetching current state after version conflict", "event_id", eventID, "session_id", sessionID, "error", getErr)
		}
		if writeErr := response.WriteError(w, http.StatusConflict, "version_conflict", "the session was modified by someone else; reload and retry", details); writeErr != nil {
			h.logger.Error("session: writing error envelope", "error", writeErr)
		}
	default:
		h.logger.Error(logMsg, "event_id", eventID, "session_id", sessionID, "error", err)
		h.writeInternalError(w)
	}
}

func (h *SessionHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	if err := response.WriteError(w, status, code, message, nil); err != nil {
		h.logger.Error("session: writing error envelope", "error", err)
	}
}

func (h *SessionHandler) writeValidationError(w http.ResponseWriter, issues map[string]any) {
	if err := response.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "the request failed validation", issues); err != nil {
		h.logger.Error("session: writing error envelope", "error", err)
	}
}

func (h *SessionHandler) writeInternalError(w http.ResponseWriter) {
	if err := response.WriteError(w, http.StatusInternalServerError, "internal", "an unexpected error occurred", nil); err != nil {
		h.logger.Error("session: writing error envelope", "error", err)
	}
}

func (h *SessionHandler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("session: encoding response", "error", err)
	}
}
