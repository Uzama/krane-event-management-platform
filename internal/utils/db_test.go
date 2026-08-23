package utils_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// Matches the Makefile's TEST_DATABASE_URL default (see migrations/schema_test.go).
const defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return defaultTestDatabaseURL
}

func TestNewPool_Succeeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := utils.NewPool(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatalf("NewPool: %v\n\n"+
			"The suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping after NewPool succeeded: %v", err)
	}
}

// TestNewPool_FailsFastOnDeadHost reproduces "routable but dead" without
// depending on how the sandbox's network handles an unroutable IP: a
// listener that never calls Accept lets the kernel complete the TCP
// handshake into the backlog (so the dial itself succeeds), but nothing ever
// speaks the Postgres protocol back. NewPool must still fail within its own
// bounded timeout rather than hang for the test's full budget.
func TestNewPool_FailsFastOnDeadHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Errorf("closing listener: %v", err)
		}
	}()

	deadURL := "postgres://user:pass@" + ln.Addr().String() + "/db?sslmode=disable"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	pool, err := utils.NewPool(ctx, deadURL)
	elapsed := time.Since(start)

	if err == nil {
		pool.Close()
		t.Fatal("NewPool succeeded against a listener that never accepts; want an error")
	}
	if elapsed > 8*time.Second {
		t.Fatalf("NewPool took %s to fail against a routable-but-dead host; want it bounded "+
			"well under the outer 15s test timeout", elapsed)
	}
}
