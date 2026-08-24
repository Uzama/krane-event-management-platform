package middleware

import (
	"log/slog"
	"net/http"

	"github.com/Uzama/krane-event-management-platform/internal/domain/authz"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

// Authz is the authorization chokepoint's enforcement half (CLAUDE.md,
// item 07 in FEATURES.md): mounted per route, parameterized by the
// resource and action that route performs, it answers policy.Can for the
// authenticated actor and the event named in the route's {eventId}
// segment. Every authz-protected route is /v1/events/{eventId}/... -- the
// permission matrix has no event:create row, since creating an event
// precedes any event_members row to check.
//
// Must be mounted after Auth: it reads the actor Auth attached to the
// context. Its absence, or a missing {eventId} path value, is a wiring
// bug -- logged and answered with 500, never conflated with a real 403.
func Authz(policy authz.Policy, resource authz.Resource, action authz.Action, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, ok := UserFromContext(r.Context())
			if !ok {
				logger.Error("authz: no authenticated user in context; Authz must be mounted after Auth")
				writeInternalError(w, logger)
				return
			}

			eventID := r.PathValue("eventId")
			if eventID == "" {
				logger.Error("authz: no eventId path value on request; route is not /v1/events/{eventId}/...", "path", r.URL.Path)
				writeInternalError(w, logger)
				return
			}

			allowed, err := policy.Can(r.Context(), actor.ID, eventID, action, resource)
			if err != nil {
				logger.Error("authz: policy check failed", "actor_id", actor.ID, "event_id", eventID, "resource", resource, "action", action, "error", err)
				writeInternalError(w, logger)
				return
			}
			if !allowed {
				logger.Warn("authz: denied", "actor_id", actor.ID, "event_id", eventID, "resource", resource, "action", action)
				writeForbidden(w, logger)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeForbidden never names what exists -- a non-member and a
// non-existent event both deny identically upstream in policy.Can, and the
// response must stay just as uninformative.
func writeForbidden(w http.ResponseWriter, logger *slog.Logger) {
	if err := response.WriteError(w, http.StatusForbidden, "forbidden", "you do not have permission to perform this action", nil); err != nil {
		logger.Error("authz: writing error envelope", "error", err)
	}
}

func writeInternalError(w http.ResponseWriter, logger *slog.Logger) {
	if err := response.WriteError(w, http.StatusInternalServerError, "internal", "an unexpected error occurred", nil); err != nil {
		logger.Error("authz: writing error envelope", "error", err)
	}
}
