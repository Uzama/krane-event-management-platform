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
	if c.AuthVerifier == nil {
		t.Error("container.AuthVerifier is nil -- item 06 must wire the mock OIDC issuer at boot")
	}
	if c.Users == nil {
		t.Error("container.Users is nil -- item 06 must wire the user upsert service at boot")
	}
	if c.Authz == nil {
		t.Error("container.Authz is nil -- item 07 must wire the role_permissions policy at boot")
	}
	if c.Events == nil {
		t.Error("container.Events is nil -- item 08 must wire the event service at boot")
	}
	if c.Members == nil {
		t.Error("container.Members is nil -- item 09 must wire the member service at boot")
	}
}

// TestNew_FailsFastOnDeadOIDCIssuer mirrors TestNew_FailsFastOnDeadHost for
// the DB pool: a bad OIDCIssuerURL must fail container.New at boot (OIDC
// discovery happens in New), not surface later on the first request.
func TestNew_FailsFastOnDeadOIDCIssuer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Errorf("closing listener: %v", err)
		}
	}()

	cfg := testConfig(t)
	cfg.OIDCIssuerURL = "http://" + ln.Addr().String() + "/default"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	c, err := container.New(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		c.Close()
		t.Fatal("container.New succeeded against an OIDC issuer that never responds; want an error")
	}
	if elapsed > 8*time.Second {
		t.Fatalf("container.New took %s to fail against a dead OIDC issuer; want it bounded", elapsed)
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
