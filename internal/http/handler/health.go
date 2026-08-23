// Package handler holds one handler per aggregate -- thin: decode, call
// service, encode. Health has no aggregate; it lives here as the one
// infrastructure endpoint the delivery layer exposes on its own behalf.
package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

// Pinger is the narrowest seam a health check needs against the db pool.
// HealthHandler depends on this, never on pgx directly, so http never
// imports the adapter layer's concrete types even transitively -- every
// later handler should depend on an interface this narrow, not a concrete
// adapter type.
type Pinger interface {
	Ping(ctx context.Context) error
}

const pingTimeout = 2 * time.Second

// HealthHandler proves the process is up and the database is reachable.
type HealthHandler struct {
	db     Pinger
	logger *slog.Logger
}

func NewHealthHandler(db Pinger, logger *slog.Logger) *HealthHandler {
	return &HealthHandler{db: db, logger: logger}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		if writeErr := response.WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "database unreachable", nil); writeErr != nil {
			h.logger.Error("health: writing error envelope", "error", writeErr)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "database": "ok"}); err != nil {
		h.logger.Error("health: encoding response", "error", err)
	}
}
