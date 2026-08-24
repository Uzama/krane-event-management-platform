package middleware_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	adapterauthz "github.com/Uzama/krane-event-management-platform/internal/adapter/authz"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/authz"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
)

// This file proves the real chain end to end -- the real go-oidc Verifier,
// the real user.Service, the real adapter/authz.Policy against krane_test,
// chained Auth -> Authz in front of a throwaway protected handler that
// exists only in this test -- nothing here touches router.go, because no
// business route consumes authz yet (items 08/09). Same shape as
// auth_integration_test.go's realStackHandler for item 06.

const reachedBody = "reached-through-auth-and-authz"

// reachedHandler writes a distinctive, unstubbable body -- every 200
// assertion below checks this body too, proving the request actually
// traversed Auth -> Authz -> the handler, not that some earlier branch
// happened to also return 200.
func reachedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(reachedBody))
	})
}

// realAuthzStack builds Auth -> Authz -> reachedHandler entirely from real
// dependencies: the real mock OIDC issuer, the real krane_test database,
// and a real adapter/authz.Policy loaded from the migration-seeded
// role_permissions table.
func realAuthzStack(t *testing.T, resource authz.Resource, action authz.Action) (http.Handler, *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifier, err := auth.New(ctx, oidcIssuerURL(), testAudience)
	if err != nil {
		t.Fatalf("auth.New against %q: %v\n\nThe suite needs the mock OIDC issuer. Run `make up` first, or `make test`, which does it for you.", oidcIssuerURL(), err)
	}

	pool, err := pgxpool.New(ctx, testDatabaseURL())
	if err != nil {
		t.Fatalf("pgxpool.New: %v\n\nThe suite needs Postgres. Run `make up` first, or `make test`, which does it for you.", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}
	t.Cleanup(pool.Close)

	users := user.NewService(postgres.NewUserRepository(pool))
	policy, err := adapterauthz.New(ctx, pool)
	if err != nil {
		t.Fatalf("adapterauthz.New: %v\n\nThe suite needs migrations/20260824090000_seed_role_permissions.up.sql applied -- run `make up`.", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mux := http.NewServeMux()
	mux.Handle("POST /v1/events/{eventId}/members/role",
		middleware.Auth(verifier, users, logger)(
			middleware.Authz(policy, resource, action, logger)(reachedHandler()),
		),
	)

	return mux, pool
}

func seedITEvent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO events (name, timezone, starts_at, ends_at)
		 VALUES ($1, 'UTC', now(), now() + interval '1 day')
		 RETURNING id::text`,
		"Authz Integration Event "+t.Name(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding event: %v", err)
	}
	return id
}

func seedITMember(t *testing.T, pool *pgxpool.Pool, eventID, subject, role string) string {
	t.Helper()

	users := user.NewService(postgres.NewUserRepository(pool))
	u, err := users.GetOrCreateBySubject(context.Background(), subject, subject+"@test.krane", "Authz IT User")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	_, err = pool.Exec(context.Background(),
		`INSERT INTO event_members (event_id, user_id, role) VALUES ($1, $2, $3)`,
		eventID, u.ID, role,
	)
	if err != nil {
		t.Fatalf("seeding event_members: %v", err)
	}
	return u.ID
}

func doAuthzRequest(t *testing.T, handler http.Handler, eventID, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/events/"+eventID+"/members/role", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestAuthzIntegration_Contributor_AssignRole_Returns403 is item 07's named
// must: test -- a contributor gets 403 attempting a role change.
func TestAuthzIntegration_Contributor_AssignRole_Returns403(t *testing.T) {
	handler, pool := realAuthzStack(t, authz.ResourceMember, authz.ActionAssignRole)
	eventID := seedITEvent(t, pool)
	subject := uniqueTestSub(t)
	seedITMember(t, pool, eventID, subject, "contributor")

	rec := doAuthzRequest(t, handler, eventID, mintToken(t, subject, ""))

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == reachedBody {
		t.Error("response body is the protected handler's body; want the request rejected before reaching it")
	}
}

func TestAuthzIntegration_Admin_AssignRole_Returns200AndReachesHandler(t *testing.T) {
	handler, pool := realAuthzStack(t, authz.ResourceMember, authz.ActionAssignRole)
	eventID := seedITEvent(t, pool)
	subject := uniqueTestSub(t)
	seedITMember(t, pool, eventID, subject, "admin")

	rec := doAuthzRequest(t, handler, eventID, mintToken(t, subject, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != reachedBody {
		t.Errorf("got body %q, want %q -- request must actually reach the handler through Auth -> Authz", rec.Body.String(), reachedBody)
	}
}

func TestAuthzIntegration_Attendee_SessionCreate_Returns403(t *testing.T) {
	handler, pool := realAuthzStack(t, authz.ResourceSession, authz.ActionCreate)
	eventID := seedITEvent(t, pool)
	subject := uniqueTestSub(t)
	seedITMember(t, pool, eventID, subject, "attendee")

	rec := doAuthzRequest(t, handler, eventID, mintToken(t, subject, ""))

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthzIntegration_Contributor_SessionCreate_Returns200AndReachesHandler(t *testing.T) {
	handler, pool := realAuthzStack(t, authz.ResourceSession, authz.ActionCreate)
	eventID := seedITEvent(t, pool)
	subject := uniqueTestSub(t)
	seedITMember(t, pool, eventID, subject, "contributor")

	rec := doAuthzRequest(t, handler, eventID, mintToken(t, subject, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != reachedBody {
		t.Errorf("got body %q, want %q -- request must actually reach the handler through Auth -> Authz", rec.Body.String(), reachedBody)
	}
}

// TestAuthzIntegration_NonExistentEvent_Returns403NotFound is the
// deliberate, documented non-leak case proved at the full-stack level: an
// authenticated user hitting a random, non-existent event id must get 403,
// never 404 -- a 404 would tell a caller with no standing that the event
// doesn't exist, which is itself information they aren't entitled to.
func TestAuthzIntegration_NonExistentEvent_Returns403NotFound(t *testing.T) {
	handler, _ := realAuthzStack(t, authz.ResourceMember, authz.ActionAssignRole)
	subject := uniqueTestSub(t)
	// A real user, just never made a member of any event.
	nonExistentEventID := fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)

	rec := doAuthzRequest(t, handler, nonExistentEventID, mintToken(t, subject, ""))

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403 (never 404) for a non-existent event", rec.Code)
	}
}
