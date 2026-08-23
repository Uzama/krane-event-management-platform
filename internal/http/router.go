// Package http is the delivery layer: server construction, the router, and
// (in its subpackages) middleware, handlers, request/response DTOs, and the
// OpenAPI validator. It depends on domain interfaces only, never on adapter.
package http

import (
	"net/http"

	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

// NewRouter is the API surface: routes -> handlers. Go 1.22+'s
// net/http.ServeMux supports method+pattern routes natively, so no router
// dependency is needed for a health-only surface (see FEATURES.md item 24 --
// this choice gets revisited before item 05 if oapi-codegen wants otherwise).
func NewRouter(health *handler.HealthHandler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", health)
	return mux
}
