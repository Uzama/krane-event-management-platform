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
// dependency is needed for a health-only surface. Item 05 confirmed this
// choice rather than reopening it: oapi-codegen's std-http-server generator
// target (internal/http/gen) produces a ServerInterface + HandlerFromMux
// that targets this exact stdlib ServeMux, and OpenAPI contract validation
// is wired into tests (internal/http/validator), not into this production
// request path -- see FEATURES.md item 05.
func NewRouter(health *handler.HealthHandler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", health)
	return mux
}
