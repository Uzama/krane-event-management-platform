package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
	"github.com/Uzama/krane-event-management-platform/internal/http/request"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// MemberService is the narrowest seam MemberHandler needs against
// domain/member.Service, matching EventService's pattern -- http depends on
// this interface, never on the concrete service type.
type MemberService interface {
	CreateMember(ctx context.Context, actorID, eventID string, in member.CreateInput) (member.Member, error)
	ListMembers(ctx context.Context, eventID string, limit int, after *member.Cursor) (member.Page, error)
	AssignRole(ctx context.Context, actorID, eventID, memberID string, version int, role string) (member.Member, error)
	RemoveMember(ctx context.Context, actorID, eventID, memberID string, version int) error
}

// MemberHandler is thin: decode, validate, call the service, encode. Every
// invariant lives in the request DTO (validation) or the repository
// (privilege guard, last-admin protection, version-gating) -- not here.
type MemberHandler struct {
	service MemberService
	logger  *slog.Logger
}

func NewMemberHandler(service MemberService, logger *slog.Logger) *MemberHandler {
	return &MemberHandler{service: service, logger: logger}
}

// CreateMember handles POST /v1/events/{eventId}/members, mounted behind
// Authz(member, create).
func (h *MemberHandler) CreateMember(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("member: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("member: no eventId path value; route is not /v1/events/{eventId}/members")
		h.writeInternalError(w)
		return
	}

	var req request.AddMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	callerRole, ok := middleware.RoleFromContext(r.Context())
	if !ok {
		h.logger.Error("member: no caller role in context; Authz must run before this handler")
		h.writeInternalError(w)
		return
	}

	created, err := h.service.CreateMember(r.Context(), actor.ID, eventID, req.ToCreateInput())
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			h.writeError(w, http.StatusNotFound, "user_not_found", "no user has signed in with that email")
		case errors.Is(err, domain.ErrForbidden):
			h.writeError(w, http.StatusForbidden, "cannot_assign_role", "you cannot grant that role")
		case errors.Is(err, domain.ErrConflict):
			h.writeError(w, http.StatusConflict, "already_member", "that user is already a member of this event")
		default:
			h.logger.Error("member: creating", "event_id", eventID, "error", err)
			h.writeInternalError(w)
		}
		return
	}

	h.writeJSON(w, http.StatusCreated, response.NewMemberResponse(created, callerRole))
}

// ListMembers handles GET /v1/events/{eventId}/members, mounted behind
// Authz(member, read).
func (h *MemberHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventId")
	if eventID == "" {
		h.logger.Error("member: no eventId path value; route is not /v1/events/{eventId}/members")
		h.writeInternalError(w)
		return
	}

	callerRole, ok := middleware.RoleFromContext(r.Context())
	if !ok {
		h.logger.Error("member: no caller role in context; Authz must run before this handler")
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

	var after *member.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		createdAt, id, err := utils.DecodeCursor(raw)
		if err != nil {
			if writeErr := response.WriteError(w, http.StatusBadRequest, "invalid_cursor", "the cursor query parameter is malformed", nil); writeErr != nil {
				h.logger.Error("member: writing error envelope", "error", writeErr)
			}
			return
		}
		after = &member.Cursor{CreatedAt: createdAt, ID: id}
	}

	page, err := h.service.ListMembers(r.Context(), eventID, limit, after)
	if err != nil {
		h.logger.Error("member: listing", "event_id", eventID, "error", err)
		h.writeInternalError(w)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewMemberListResponse(page, callerRole))
}

// AssignRole handles PATCH /v1/events/{eventId}/members/{memberId}, mounted
// behind Authz(member, assign-role).
func (h *MemberHandler) AssignRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("member: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	memberID := r.PathValue("memberId")
	if eventID == "" || memberID == "" {
		h.logger.Error("member: missing eventId or memberId path value; route is not /v1/events/{eventId}/members/{memberId}")
		h.writeInternalError(w)
		return
	}

	callerRole, ok := middleware.RoleFromContext(r.Context())
	if !ok {
		h.logger.Error("member: no caller role in context; Authz must run before this handler")
		h.writeInternalError(w)
		return
	}

	var req request.AssignRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeValidationError(w, map[string]any{"body": "must be valid JSON"})
		return
	}
	if issues := req.Validate(); len(issues) > 0 {
		h.writeValidationError(w, issues)
		return
	}

	updated, err := h.service.AssignRole(r.Context(), actor.ID, eventID, memberID, req.Version, req.Role)
	if err != nil {
		h.writeAssignOrRemoveError(w, "member: assigning role", eventID, memberID, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response.NewMemberResponse(updated, callerRole))
}

// RemoveMember handles DELETE /v1/events/{eventId}/members/{memberId}?version=N,
// mounted behind Authz(member, delete). version is a required query
// parameter, matching DeleteEvent's interim convention pending item 17's
// If-Match design.
func (h *MemberHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		h.logger.Error("member: no authenticated user in context; Auth must run before this handler")
		h.writeInternalError(w)
		return
	}

	eventID := r.PathValue("eventId")
	memberID := r.PathValue("memberId")
	if eventID == "" || memberID == "" {
		h.logger.Error("member: missing eventId or memberId path value; route is not /v1/events/{eventId}/members/{memberId}")
		h.writeInternalError(w)
		return
	}

	raw := r.URL.Query().Get("version")
	version, err := strconv.Atoi(raw)
	if raw == "" || err != nil || version <= 0 {
		h.writeValidationError(w, map[string]any{"version": "is required and must be a positive integer"})
		return
	}

	if err := h.service.RemoveMember(r.Context(), actor.ID, eventID, memberID, version); err != nil {
		h.writeAssignOrRemoveError(w, "member: removing", eventID, memberID, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeAssignOrRemoveError maps AssignRole/RemoveMember's shared error
// vocabulary: ErrConflict here always means the last-admin guard blocked a
// state-changing write, distinct from CreateMember's use of the same
// sentinel for "already a member" -- a state conflict, not a permission
// failure, so 409 not 403.
func (h *MemberHandler) writeAssignOrRemoveError(w http.ResponseWriter, logMsg, eventID, memberID string, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "no such member")
	case errors.Is(err, domain.ErrVersionMismatch):
		h.writeError(w, http.StatusConflict, "version_conflict", "the member was modified by someone else; reload and retry")
	case errors.Is(err, domain.ErrConflict):
		h.writeError(w, http.StatusConflict, "last_admin", "this event must always have at least one admin")
	default:
		h.logger.Error(logMsg, "event_id", eventID, "member_id", memberID, "error", err)
		h.writeInternalError(w)
	}
}

func (h *MemberHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	if err := response.WriteError(w, status, code, message, nil); err != nil {
		h.logger.Error("member: writing error envelope", "error", err)
	}
}

func (h *MemberHandler) writeValidationError(w http.ResponseWriter, issues map[string]any) {
	if err := response.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "the request failed validation", issues); err != nil {
		h.logger.Error("member: writing error envelope", "error", err)
	}
}

func (h *MemberHandler) writeInternalError(w http.ResponseWriter) {
	if err := response.WriteError(w, http.StatusInternalServerError, "internal", "an unexpected error occurred", nil); err != nil {
		h.logger.Error("member: writing error envelope", "error", err)
	}
}

func (h *MemberHandler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.Error("member: encoding response", "error", err)
	}
}
