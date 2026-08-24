// Package bootstrap runs the composition container builds: load config,
// build the container, mount the router, listen, graceful shutdown.
package bootstrap

import (
	"context"
	"fmt"
	"net"

	"github.com/Uzama/krane-event-management-platform/internal/container"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// Boot loads config, builds the container, mounts the router, and serves
// until ctx is cancelled. It is the one place cmd/api calls into.
func Boot(ctx context.Context) error {
	cfg := utils.Load()

	c, err := container.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("building container: %w", err)
	}
	defer c.Close()

	router := apihttp.NewRouter(apihttp.RouterDeps{
		Health:       handler.NewHealthHandler(c.DB, c.Logger),
		Event:        handler.NewEventHandler(c.Events, c.Logger),
		AuthVerifier: c.AuthVerifier,
		Users:        c.Users,
		Authz:        c.Authz,
		Logger:       c.Logger,
	})
	srv := apihttp.NewServer(cfg, router)

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", srv.Addr, err)
	}

	c.Logger.Info("listening", "addr", ln.Addr().String())

	return apihttp.Run(ctx, srv, ln, c.Logger)
}
