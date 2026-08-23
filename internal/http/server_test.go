package http_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

func TestNewServer_Timeouts(t *testing.T) {
	srv := apihttp.NewServer(utils.Config{HTTPPort: "8080"}, http.NewServeMux())

	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout must be set to bound slow-header attacks")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout must be set")
	}
	if srv.WriteTimeout <= 0 {
		t.Error("WriteTimeout must be set")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout must be set")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitUntilServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			if cerr := conn.Close(); cerr != nil {
				t.Errorf("closing probe connection: %v", cerr)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never started accepting connections", addr)
}

// TestRun_GracefulShutdown proves shutdown is real, not just "Run returns
// nil": the server must actually be reachable before cancellation and
// actually stop accepting connections after Run returns.
func TestRun_GracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: mux}

	ctx, cancel := context.WithCancel(context.Background())

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- apihttp.Run(ctx, srv, ln, discardLogger())
	}()

	waitUntilServing(t, addr)

	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("GET /ping while serving: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("closing response body: %v", cerr)
	}

	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("Run returned error after shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context cancellation")
	}

	if resp2, err := http.Get("http://" + addr + "/ping"); err == nil {
		t.Error("expected an error hitting the server after Run returned; the port should no longer accept connections")
		if cerr := resp2.Body.Close(); cerr != nil {
			t.Errorf("closing response body: %v", cerr)
		}
	}
}
