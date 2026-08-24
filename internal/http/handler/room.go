package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
	"github.com/Uzama/krane-event-management-platform/internal/http/request"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// RoomService is the narrowest seam RoomHandler needs against
// domain/room.Service, matching EventService's pattern -- http depends on
// this interface, never on the concrete service type.
type RoomService interface {
	CreateRoom(ctx context.Context, actorID, eventID string, in room.CreateInput) (room.Room, error)
	GetRoom(ctx context.Context, eventID, roomID string) (room.Room, error)
	ListRooms(ctx context.Context, eventID string, limit int, after *room.Cursor) (room.Page, error)
	UpdateRoom(ctx context.Context, actorID, eventID, roomID string, version int, patch room.Patch) (room.Room, error)
	DeleteRoom(ctx context.Context, actorID, eventID, roomID string, version int) error
}

// RoomHandler is thin: decode, validate, call the service, encode. Every
// invariant lives in the request DTO (validation) or the repository
// (uniqueness, version-gating, the sessions-FK delete guard) -- not here.
type RoomHandler struct {
	service RoomService
	logger  *slog.Logger
}

func NewRoomHandler(service RoomService, logger *slog.Logger) *RoomHandler {
	return &RoomHandler{service: service, logger: logger}
}

// CreateRoom handles POST /v1/events/{eventId}/rooms, mounted behind
// Authz(room, create).
func (h *RoomHandler) CreateRoom(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("room: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("room: no eventId path value; route is not /v1/events/{eventId}/rooms")
		h.writeInternalError(w)
		return
	}

	var req request.CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	created, err := h.service.CreateRoom(r.Context(), actor.ID, eventID, req.ToCreateInput())
	if err != nil {
		h.writeCreateOrUpdateError(w, "room: creating", eventID, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, response.NewRoomResponse(created))
}

// GetRoom handles GET /v1/events/{eventId}/rooms/{roomId}, mounted behind
// Authz(room, read).
func (h *RoomHandler) GetRoom(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	roomID := r.PathValue("roomId")
	if eventID == "" || roomID == "" {
		h.logger.Error("room: missing eventId or roomId path value; route is not /v1/events/{eventId}/rooms/{roomId}")
		h.writeInternalError(w)
		return
	}

	got, err := h.service.GetRoom(r.Context(), eventID, roomID)
	if err != nil {
		h.writeNotFoundOrInternal(w, "room: getting", eventID, roomID, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewRoomResponse(got))
}

// ListRooms handles GET /v1/events/{eventId}/rooms, mounted behind
// Authz(room, read).
func (h *RoomHandler) ListRooms(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("room: no eventId path value; route is not /v1/events/{eventId}/rooms")
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

	var after *room.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		createdAt, id, err := utils.DecodeCursor(raw)
		if err != nil {
			if writeErr := response.WriteError(w, http.StatusBadRequest, "invalid_cursor", "the cursor query parameter is malformed", nil); writeErr != nil {
				h.logger.Error("room: writing error envelope", "error", writeErr)
			}
			return
		}
		after = &room.Cursor{CreatedAt: createdAt, ID: id}
	}

	page, err := h.service.ListRooms(r.Context(), eventID, limit, after)
	if err != nil {
		h.logger.Error("room: listing", "event_id", eventID, "error", err)
		h.writeInternalError(w)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewRoomListResponse(page))
}

// PatchRoom handles PATCH /v1/events/{eventId}/rooms/{roomId}, mounted
// behind Authz(room, update).
func (h *RoomHandler) PatchRoom(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("room: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	roomID := r.PathValue("roomId")
	if eventID == "" || roomID == "" {
		h.logger.Error("room: missing eventId or roomId path value; route is not /v1/events/{eventId}/rooms/{roomId}")
		h.writeInternalError(w)
		return
	}

	var req request.PatchRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	updated, err := h.service.UpdateRoom(r.Context(), actor.ID, eventID, roomID, req.Version, req.ToPatch())
	if err != nil {
		h.writeUpdateOrDeleteError(w, r.Context(), "room: updating", eventID, roomID, err, "room_name_taken", "another room in this event already has that name")
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewRoomResponse(updated))
}

// DeleteRoom handles DELETE /v1/events/{eventId}/rooms/{roomId}?version=N,
// mounted behind Authz(room, delete). version is a required query
// parameter -- item 17 kept this design (query-param on DELETE, body on
// PATCH) permanently rather than retrofitting ETag/If-Match; see event.go's
// DeleteEvent comment.
func (h *RoomHandler) DeleteRoom(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("room: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	roomID := r.PathValue("roomId")
	if eventID == "" || roomID == "" {
		h.logger.Error("room: missing eventId or roomId path value; route is not /v1/events/{eventId}/rooms/{roomId}")
		h.writeInternalError(w)
		return
	}

	raw := r.URL.Query().Get("version")
	version, err := strconv.Atoi(raw)
	if raw == "" || err != nil || version <= 0 {
		h.writeValidationError(w, map[string]any{"version": "is required and must be a positive integer"})
		return
	}

	if err := h.service.DeleteRoom(r.Context(), actor.ID, eventID, roomID, version); err != nil {
		h.writeUpdateOrDeleteError(w, r.Context(), "room: deleting", eventID, roomID, err, "room_in_use", "this room has sessions; remove them before deleting the room")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeCreateOrUpdateError maps CreateRoom's error vocabulary: ErrConflict
// here always means a unique-name collision within the event.
func (h *RoomHandler) writeCreateOrUpdateError(w http.ResponseWriter, logMsg, eventID string, err error) {
	if errors.Is(err, domain.ErrConflict) {
		h.writeError(w, http.StatusConflict, "room_name_taken", "another room in this event already has that name")
		return
	}
	h.logger.Error(logMsg, "event_id", eventID, "error", err)
	h.writeInternalError(w)
}

// writeNotFoundOrInternal handles GetRoom's error vocabulary -- Get only
// ever returns ErrNotFound or a genuine failure.
func (h *RoomHandler) writeNotFoundOrInternal(w http.ResponseWriter, logMsg, eventID, roomID string, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "not_found", "no such room")
		return
	}
	h.logger.Error(logMsg, "event_id", eventID, "room_id", roomID, "error", err)
	h.writeInternalError(w)
}

// writeUpdateOrDeleteError maps PatchRoom/DeleteRoom's shared vocabulary.
// ErrConflict's meaning differs by call site -- a rename collision for
// PatchRoom, the sessions-FK guard for DeleteRoom -- so the caller supplies
// the code and message for that case. ErrVersionMismatch (item 17) embeds
// the room's current state under details.current, same as event.go's
// writeDomainError.
func (h *RoomHandler) writeUpdateOrDeleteError(w http.ResponseWriter, ctx context.Context, logMsg, eventID, roomID string, err error, conflictCode, conflictMessage string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "no such room")
	case errors.Is(err, domain.ErrVersionMismatch):
		details := map[string]any{"current": nil}
		switch current, getErr := h.service.GetRoom(ctx, eventID, roomID); {
		case getErr == nil:
			details["current"] = response.NewRoomResponse(current)
		case errors.Is(getErr, domain.ErrNotFound):
			// Winning writer deleted the room between the failed write and
			// this re-fetch -- expected, not a bug. Stays explicitly null.
		default:
			h.logger.Error("room: fetching current state after version conflict", "event_id", eventID, "room_id", roomID, "error", getErr)
		}
		if writeErr := response.WriteError(w, http.StatusConflict, "version_conflict", "the room was modified by someone else; reload and retry", details); writeErr != nil {
			h.logger.Error("room: writing error envelope", "error", writeErr)
		}
	case errors.Is(err, domain.ErrConflict):
		h.writeError(w, http.StatusConflict, conflictCode, conflictMessage)
	default:
		h.logger.Error(logMsg, "event_id", eventID, "room_id", roomID, "error", err)
		h.writeInternalError(w)
	}
}

func (h *RoomHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	if err := response.WriteError(w, status, code, message, nil); err != nil {
		h.logger.Error("room: writing error envelope", "error", err)
	}
}

func (h *RoomHandler) writeValidationError(w http.ResponseWriter, issues map[string]any) {
	if err := response.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "the request failed validation", issues); err != nil {
		h.logger.Error("room: writing error envelope", "error", err)
	}
}

func (h *RoomHandler) writeInternalError(w http.ResponseWriter) {
	if err := response.WriteError(w, http.StatusInternalServerError, "internal", "an unexpected error occurred", nil); err != nil {
		h.logger.Error("room: writing error envelope", "error", err)
	}
}

func (h *RoomHandler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("room: encoding response", "error", err)
	}
}
