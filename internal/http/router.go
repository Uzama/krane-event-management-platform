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
	Health  *handler.HealthHandler
	Event   *handler.EventHandler
	Member  *handler.MemberHandler
	Room    *handler.RoomHandler
	Session *handler.SessionHandler

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
//
// The four /v1/events/{eventId}/members routes (item 09) are all Auth then
// Authz(member, ...), one per role_permissions action: create (add a
// member), read (the roster), assign-role (change an existing member's
// role -- its own action, distinct from a generic member:update, since
// contributor may create but never assign-role), and delete.
//
// The five /v1/events/{eventId}/rooms routes (item 11) are all Auth then
// Authz(room, ...) -- unlike events' create/list, role_permissions already
// has room:create and room:read rows for admin/contributor, so there is no
// events-style Auth-only carve-out here; every room route goes through the
// chokepoint, matching the members pattern.
//
// The five /v1/events/{eventId}/sessions routes (item 12) are the same
// shape as rooms -- role_permissions already has session rows for all
// three roles (item 07 seed, including attendee:session:read), so every
// route goes through Authz, no Auth-only carve-out.
func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", deps.Health)

	auth := middleware.Auth(deps.AuthVerifier, deps.Users, deps.Logger)
	authzFor := func(action domainauthz.Action) func(http.Handler) http.Handler {
		return middleware.Authz(deps.Authz, domainauthz.ResourceEvent, action, deps.Logger)
	}
	authzForMember := func(action domainauthz.Action) func(http.Handler) http.Handler {
		return middleware.Authz(deps.Authz, domainauthz.ResourceMember, action, deps.Logger)
	}
	authzForRoom := func(action domainauthz.Action) func(http.Handler) http.Handler {
		return middleware.Authz(deps.Authz, domainauthz.ResourceRoom, action, deps.Logger)
	}
	authzForSession := func(action domainauthz.Action) func(http.Handler) http.Handler {
		return middleware.Authz(deps.Authz, domainauthz.ResourceSession, action, deps.Logger)
	}

	mux.Handle("POST /v1/events", auth(http.HandlerFunc(deps.Event.CreateEvent)))
	mux.Handle("GET /v1/events", auth(http.HandlerFunc(deps.Event.ListEvents)))
	mux.Handle("GET /v1/events/{eventId}", auth(authzFor(domainauthz.ActionRead)(http.HandlerFunc(deps.Event.GetEvent))))
	mux.Handle("PATCH /v1/events/{eventId}", auth(authzFor(domainauthz.ActionUpdate)(http.HandlerFunc(deps.Event.PatchEvent))))
	mux.Handle("DELETE /v1/events/{eventId}", auth(authzFor(domainauthz.ActionDelete)(http.HandlerFunc(deps.Event.DeleteEvent))))

	mux.Handle("POST /v1/events/{eventId}/members", auth(authzForMember(domainauthz.ActionCreate)(http.HandlerFunc(deps.Member.CreateMember))))
	mux.Handle("GET /v1/events/{eventId}/members", auth(authzForMember(domainauthz.ActionRead)(http.HandlerFunc(deps.Member.ListMembers))))
	mux.Handle("PATCH /v1/events/{eventId}/members/{memberId}", auth(authzForMember(domainauthz.ActionAssignRole)(http.HandlerFunc(deps.Member.AssignRole))))
	mux.Handle("DELETE /v1/events/{eventId}/members/{memberId}", auth(authzForMember(domainauthz.ActionDelete)(http.HandlerFunc(deps.Member.RemoveMember))))

	mux.Handle("POST /v1/events/{eventId}/rooms", auth(authzForRoom(domainauthz.ActionCreate)(http.HandlerFunc(deps.Room.CreateRoom))))
	mux.Handle("GET /v1/events/{eventId}/rooms", auth(authzForRoom(domainauthz.ActionRead)(http.HandlerFunc(deps.Room.ListRooms))))
	mux.Handle("GET /v1/events/{eventId}/rooms/{roomId}", auth(authzForRoom(domainauthz.ActionRead)(http.HandlerFunc(deps.Room.GetRoom))))
	mux.Handle("PATCH /v1/events/{eventId}/rooms/{roomId}", auth(authzForRoom(domainauthz.ActionUpdate)(http.HandlerFunc(deps.Room.PatchRoom))))
	mux.Handle("DELETE /v1/events/{eventId}/rooms/{roomId}", auth(authzForRoom(domainauthz.ActionDelete)(http.HandlerFunc(deps.Room.DeleteRoom))))

	mux.Handle("POST /v1/events/{eventId}/sessions", auth(authzForSession(domainauthz.ActionCreate)(http.HandlerFunc(deps.Session.CreateSession))))
	mux.Handle("GET /v1/events/{eventId}/sessions", auth(authzForSession(domainauthz.ActionRead)(http.HandlerFunc(deps.Session.ListSessions))))
	mux.Handle("GET /v1/events/{eventId}/sessions/{sessionId}", auth(authzForSession(domainauthz.ActionRead)(http.HandlerFunc(deps.Session.GetSession))))
	mux.Handle("PATCH /v1/events/{eventId}/sessions/{sessionId}", auth(authzForSession(domainauthz.ActionUpdate)(http.HandlerFunc(deps.Session.PatchSession))))
	mux.Handle("DELETE /v1/events/{eventId}/sessions/{sessionId}", auth(authzForSession(domainauthz.ActionDelete)(http.HandlerFunc(deps.Session.DeleteSession))))

	return mux
}
