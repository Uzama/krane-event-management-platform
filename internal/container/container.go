// Package container is the composition root: it builds concrete adapters and
// injects them into domain services and into http. It may import everything;
// nothing may import it.
package container

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// Container holds every shared dependency the rest of the app is wired
// against.
type Container struct {
	Config utils.Config
	Logger *slog.Logger
	DB     *pgxpool.Pool
}

// New builds every dependency and proves the database is reachable before
// returning -- a bad DATABASE_URL fails boot here, not on the first request.
func New(ctx context.Context, cfg utils.Config) (*Container, error) {
	logger := utils.NewLogger(cfg)

	pool, err := utils.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	return &Container{
		Config: cfg,
		Logger: logger,
		DB:     pool,
	}, nil
}

// Close releases every resource the container holds.
func (c *Container) Close() {
	c.DB.Close()
}
