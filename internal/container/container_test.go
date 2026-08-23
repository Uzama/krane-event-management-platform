package container_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/container"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// Matches the Makefile's TEST_DATABASE_URL default (see migrations/schema_test.go).
const defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"

func testConfig(t *testing.T) utils.Config {
	t.Helper()
	cfg := utils.Load()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		cfg.DatabaseURL = url
	} else {
		cfg.DatabaseURL = defaultTestDatabaseURL
	}
	return cfg
}

func TestNew_Succeeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := container.New(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("container.New: %v\n\n"+
			"The suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	defer c.Close()

	if c.Logger == nil {
		t.Error("container.Logger is nil")
	}
	if c.DB == nil {
		t.Fatal("container.DB is nil")
	}
	if err := c.DB.Ping(ctx); err != nil {
		t.Errorf("container.DB.Ping: %v", err)
	}
}

// TestNew_FailsFastOnDeadHost proves container.New surfaces the pool's
// fail-fast error rather than swallowing it or hanging -- see
// utils.TestNewPool_FailsFastOnDeadHost for why this listener shape
// reproduces "routable but dead."
func TestNew_FailsFastOnDeadHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Errorf("closing listener: %v", err)
		}
	}()

	cfg := utils.Load()
	cfg.DatabaseURL = "postgres://user:pass@" + ln.Addr().String() + "/db?sslmode=disable"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	c, err := container.New(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		c.Close()
		t.Fatal("container.New succeeded against a listener that never accepts; want an error")
	}
	if elapsed > 8*time.Second {
		t.Fatalf("container.New took %s to fail against a routable-but-dead host; want it bounded", elapsed)
	}
}
