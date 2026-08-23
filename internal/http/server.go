package http

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// NewServer builds the http.Server with production-sane timeouts. Addr is
// derived from cfg but binding happens separately (net.Listen), so callers
// -- including tests -- can choose an ephemeral port.
func NewServer(cfg utils.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// Run serves srv on ln until ctx is cancelled, then shuts down gracefully.
// It is the one graceful-shutdown hook every entrypoint uses.
func Run(ctx context.Context, srv *http.Server, ln net.Listener, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}

		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
