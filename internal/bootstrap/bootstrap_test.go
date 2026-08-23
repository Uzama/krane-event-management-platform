package bootstrap_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/bootstrap"
)

// TestBoot_FailsFastOnBadDatabaseURL proves the composition itself -- config
// -> container -> router -> server -- fails fast on a bad DATABASE_URL
// before ever binding a listener or accepting a request, rather than hanging
// or serving traffic against a broken pool. Uses the same never-Accept
// listener trick as utils.TestNewPool_FailsFastOnDeadHost to reproduce
// "routable but dead" deterministically.
func TestBoot_FailsFastOnBadDatabaseURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Errorf("closing listener: %v", err)
		}
	}()

	t.Setenv("DATABASE_URL", "postgres://user:pass@"+ln.Addr().String()+"/db?sslmode=disable")
	t.Setenv("HTTP_PORT", "0")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	err = bootstrap.Boot(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Boot succeeded against a listener that never accepts; want an error")
	}
	if elapsed > 8*time.Second {
		t.Fatalf("Boot took %s to fail on a bad DATABASE_URL; want it bounded", elapsed)
	}
}
