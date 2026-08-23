package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

type fakePinger struct {
	err error
}

func (f fakePinger) Ping(ctx context.Context) error {
	return f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthHandler_DatabaseReachable(t *testing.T) {
	h := handler.NewHealthHandler(fakePinger{}, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body["status"] != "ok" || body["database"] != "ok" {
		t.Errorf("body = %+v, want status=ok database=ok", body)
	}
}

func TestHealthHandler_DatabaseUnreachable(t *testing.T) {
	h := handler.NewHealthHandler(fakePinger{err: errors.New("connection refused")}, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var decoded response.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if decoded.Error.Code != "service_unavailable" {
		t.Errorf("error.code = %q, want service_unavailable", decoded.Error.Code)
	}
}

// Matches the Makefile's TEST_DATABASE_URL default (see migrations/schema_test.go).
const defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return defaultTestDatabaseURL
}

// TestHealthHandler_Integration proves *pgxpool.Pool really satisfies
// handler.Pinger and that config -> pool -> router -> handler is wired end
// to end, against a real Postgres -- not just that the interface compiles.
func TestHealthHandler_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := utils.NewPool(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatalf("NewPool: %v\n\n"+
			"The suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	defer pool.Close()

	router := apihttp.NewRouter(handler.NewHealthHandler(pool, discardLogger()))
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("closing response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body["status"] != "ok" || body["database"] != "ok" {
		t.Errorf("body = %+v, want status=ok database=ok", body)
	}

	// Close the pool out from under the running server: the next request
	// must degrade to 503 with the standard envelope, proving the failure
	// path is really reachable through the whole stack, not just in the
	// fakePinger unit test above.
	pool.Close()

	resp2, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health after pool close: %v", err)
	}
	defer func() {
		if err := resp2.Body.Close(); err != nil {
			t.Errorf("closing response body: %v", err)
		}
	}()

	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status after pool close = %d, want %d", resp2.StatusCode, http.StatusServiceUnavailable)
	}

	var decoded response.Envelope
	if err := json.NewDecoder(resp2.Body).Decode(&decoded); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if decoded.Error.Code != "service_unavailable" {
		t.Errorf("error.code = %q, want service_unavailable", decoded.Error.Code)
	}
}
