package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
	"github.com/Uzama/krane-event-management-platform/internal/http/request"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// InvitationService is the narrowest seam InvitationHandler needs against
// domain/invitation.Service, matching MemberService's pattern -- http
// depends on this interface, never on the concrete service type.
type InvitationService interface {
	CreateInvitation(ctx context.Context, actorID, eventID string, in invitation.CreateInput) (invitation.Invitation, error)
	ListInvitations(ctx context.Context, eventID string, limit int, after *invitation.Cursor) (invitation.Page, error)
}

// InvitationHandler is thin: decode, validate, call the service, encode.
// Every invariant lives in the request DTO (shape validation) or the
// repository (the invite-role privilege guard, audit atomicity) -- not
// here.
type InvitationHandler struct {
	service InvitationService
	logger  *slog.Logger
}

func NewInvitationHandler(service InvitationService, logger *slog.Logger) *InvitationHandler {
	return &InvitationHandler{service: service, logger: logger}
}

// CreateInvitation handles POST /v1/events/{eventId}/invitations, mounted
// behind Authz(invitation, create).
func (h *InvitationHandler) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("invitation: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("invitation: no eventId path value; route is not /v1/events/{eventId}/invitations")
		h.writeInternalError(w)
		return
	}

	var req request.InvitationCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	created, err := h.service.CreateInvitation(r.Context(), actor.ID, eventID, req.ToCreateInput())
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrForbidden):
			h.writeError(w, http.StatusForbidden, "cannot_invite_at_role", "you cannot invite someone at that role")
		case errors.Is(err, domain.ErrConflict):
			h.writeError(w, http.StatusConflict, "already_invited", "this event already has an invitation for that email")
		default:
			h.logger.Error("invitation: creating", "event_id", eventID, "error", err)
			h.writeInternalError(w)
		}
		return
	}

	h.writeJSON(w, http.StatusCreated, response.NewInvitationResponse(created))
}

// ListInvitations handles GET /v1/events/{eventId}/invitations, mounted
// behind Authz(invitation, read).
func (h *InvitationHandler) ListInvitations(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("invitation: no eventId path value; route is not /v1/events/{eventId}/invitations")
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

	var after *invitation.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		createdAt, id, err := utils.DecodeCursor(raw)
		if err != nil {
			if writeErr := response.WriteError(w, http.StatusBadRequest, "invalid_cursor", "the cursor query parameter is malformed", nil); writeErr != nil {
				h.logger.Error("invitation: writing error envelope", "error", writeErr)
			}
			return
		}
		after = &invitation.Cursor{CreatedAt: createdAt, ID: id}
	}

	page, err := h.service.ListInvitations(r.Context(), eventID, limit, after)
	if err != nil {
		h.logger.Error("invitation: listing", "event_id", eventID, "error", err)
		h.writeInternalError(w)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewInvitationListResponse(page))
}

func (h *InvitationHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	if err := response.WriteError(w, status, code, message, nil); err != nil {
		h.logger.Error("invitation: writing error envelope", "error", err)
	}
}

func (h *InvitationHandler) writeValidationError(w http.ResponseWriter, issues map[string]any) {
	if err := response.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "the request failed validation", issues); err != nil {
		h.logger.Error("invitation: writing error envelope", "error", err)
	}
}

func (h *InvitationHandler) writeInternalError(w http.ResponseWriter) {
	if err := response.WriteError(w, http.StatusInternalServerError, "internal", "an unexpected error occurred", nil); err != nil {
		h.logger.Error("invitation: writing error envelope", "error", err)
	}
}

func (h *InvitationHandler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("invitation: encoding response", "error", err)
	}
}
