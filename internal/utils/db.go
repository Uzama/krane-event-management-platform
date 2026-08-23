package utils

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// connectTimeout bounds the TCP dial itself; pingTimeout bounds the whole
// round trip (dial + Postgres handshake) that NewPool performs once, up
// front, so a bad DATABASE_URL fails boot instead of silently serving
// traffic against a broken pool.
const (
	connectTimeout = 5 * time.Second
	pingTimeout    = 5 * time.Second
)

// NewPool builds a pgx connection pool and proves it actually works before
// returning it. Fails fast both on a refused connection and on a routable
// host that never responds: ConnConfig.ConnectTimeout bounds the dial, and
// the ping's own context bounds the whole handshake.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}
