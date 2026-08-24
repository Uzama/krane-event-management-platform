// Package http is the delivery layer: server construction, the router, and
// (in its subpackages) middleware, handlers, request/response DTOs, and the
// OpenAPI validator. It depends on domain interfaces only, never on adapter.
package http

import (
	"log/slog"
	"net/http"

	domainauthz "github.com/Uzama/krane-event-management-platform/internal/domain/authz"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
)

// RouterDeps is everything NewRouter needs to mount every route. A struct,
// not a growing positional parameter list, now that item 08 adds the first
// business routes alongside /health.
type RouterDeps struct {
	Health *handler.HealthHandler
	Event  *handler.EventHandler

	AuthVerifier middleware.TokenVerifier
	Users        middleware.UserResolver
	Authz        domainauthz.Policy

	Logger *slog.Logger
}

// NewRouter is the API surface: routes -> handlers. Go 1.22+'s
// net/http.ServeMux supports method+pattern routes natively, so no router
// dependency is needed (item 05 confirmed oapi-codegen's std-http-server
// target matches this exact stdlib mux). Wiring stays manual
// (mux.Handle(...)), not oapi-codegen's generated ServerInterface: PATCH's
// domain/opt.Optional[T] decoding needs hand-written request DTOs anyway,
// since encoding/json's default *T can't distinguish an absent field from
// an explicit null.
//
// POST /v1/events and GET /v1/events run behind Auth only, never Authz --
// role_permissions has no event:create row (creating an event precedes any
// event_members row to check), and list scoping is a query against
// event_members, not a per-resource Can() check (docs/requirements.md §4
// point 3). The other three routes are Auth then Authz, matching every
// other /v1/events/{eventId}/... route in this API.
func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", deps.Health)

	auth := middleware.Auth(deps.AuthVerifier, deps.Users, deps.Logger)
	authzFor := func(action domainauthz.Action) func(http.Handler) http.Handler {
		return middleware.Authz(deps.Authz, domainauthz.ResourceEvent, action, deps.Logger)
	}

	mux.Handle("POST /v1/events", auth(http.HandlerFunc(deps.Event.CreateEvent)))
	mux.Handle("GET /v1/events", auth(http.HandlerFunc(deps.Event.ListEvents)))
	mux.Handle("GET /v1/events/{eventId}", auth(authzFor(domainauthz.ActionRead)(http.HandlerFunc(deps.Event.GetEvent))))
	mux.Handle("PATCH /v1/events/{eventId}", auth(authzFor(domainauthz.ActionUpdate)(http.HandlerFunc(deps.Event.PatchEvent))))
	mux.Handle("DELETE /v1/events/{eventId}", auth(authzFor(domainauthz.ActionDelete)(http.HandlerFunc(deps.Event.DeleteEvent))))

	return mux
}
