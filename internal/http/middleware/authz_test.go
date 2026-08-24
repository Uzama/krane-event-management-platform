package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/authz"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
)

type fakePolicy struct {
	allowed bool
	role    string
	err     error
}

func (f *fakePolicy) Can(_ context.Context, _, _ string, _ authz.Action, _ authz.Resource) (bool, string, error) {
	return f.allowed, f.role, f.err
}

func okNext(t *testing.T) (http.Handler, *bool) {
	t.Helper()
	called := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), &called
}

// authzRequest builds a request through a mux with an {eventId} path
// pattern -- every authz-protected route is /v1/events/{eventId}/... (the
// matrix has no event:create row), so the middleware reads eventId via
// r.PathValue, which only ServeMux's routing populates.
func authzRequest(t *testing.T, handler http.Handler, actor *user.User) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle("POST /v1/events/{eventId}/sessions", handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", nil)
	if actor != nil {
		req = req.WithContext(middleware.ContextWithUser(req.Context(), *actor))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// roleCapturingNext records whatever role, if any, middleware.RoleFromContext
// reports when the handler runs -- lets a test prove Authz actually attached
// the role to the request context passed downstream, not just that it read
// one from the policy.
func roleCapturingNext(t *testing.T) (http.Handler, *string, *bool) {
	t.Helper()
	var gotRole string
	var ok bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRole, ok = middleware.RoleFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}), &gotRole, &ok
}

func TestAuthz_Allowed_AttachesRoleToContext(t *testing.T) {
	next, gotRole, ok := roleCapturingNext(t)
	handler := middleware.Authz(&fakePolicy{allowed: true, role: "contributor"}, authz.ResourceSession, authz.ActionCreate, testLogger())(next)

	rec := authzRequest(t, handler, &user.User{ID: "user-1"})

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !*ok {
		t.Fatal("RoleFromContext found no role; want Authz to attach one on success")
	}
	if *gotRole != "contributor" {
		t.Errorf("got role %q, want contributor", *gotRole)
	}
}

func TestAuthz_Allowed_CallsNext(t *testing.T) {
	next, called := okNext(t)
	handler := middleware.Authz(&fakePolicy{allowed: true}, authz.ResourceSession, authz.ActionCreate, testLogger())(next)

	rec := authzRequest(t, handler, &user.User{ID: "user-1"})

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !*called {
		t.Error("next handler was not called, want it called when Can returns allowed=true")
	}
}

func TestAuthz_Denied_Returns403NotCallingNext(t *testing.T) {
	next, called := okNext(t)
	handler := middleware.Authz(&fakePolicy{allowed: false}, authz.ResourceMember, authz.ActionAssignRole, testLogger())(next)

	rec := authzRequest(t, handler, &user.User{ID: "user-1"})

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403", rec.Code)
	}
	if *called {
		t.Error("next handler was called; want request rejected before reaching it")
	}
}

func TestAuthz_PolicyError_Returns500NotCallingNext(t *testing.T) {
	next, called := okNext(t)
	handler := middleware.Authz(&fakePolicy{err: errors.New("db unreachable")}, authz.ResourceSession, authz.ActionCreate, testLogger())(next)

	rec := authzRequest(t, handler, &user.User{ID: "user-1"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500 for a policy error", rec.Code)
	}
	if *called {
		t.Error("next handler was called; want request rejected before reaching it")
	}
}

func TestAuthz_NoActorInContext_Returns500NotCallingNext(t *testing.T) {
	next, called := okNext(t)
	handler := middleware.Authz(&fakePolicy{allowed: true}, authz.ResourceSession, authz.ActionCreate, testLogger())(next)

	// No actor attached -- Authz must run after Auth; its absence is a
	// wiring bug, not a client-facing 401/403.
	rec := authzRequest(t, handler, nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500 when no authenticated user is in context", rec.Code)
	}
	if *called {
		t.Error("next handler was called; want request rejected before reaching it")
	}
}

func TestAuthz_NoEventIDPathValue_Returns500NotCallingNext(t *testing.T) {
	next, called := okNext(t)
	handler := middleware.Authz(&fakePolicy{allowed: true}, authz.ResourceSession, authz.ActionCreate, testLogger())(next)

	// A route with no {eventId} segment is a wiring bug -- every
	// authz-protected route is /v1/events/{eventId}/... (the matrix has no
	// event:create row).
	mux := http.NewServeMux()
	mux.Handle("POST /v1/misconfigured", handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/misconfigured", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), user.User{ID: "user-1"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500 when the route has no eventId path value", rec.Code)
	}
	if *called {
		t.Error("next handler was called; want request rejected before reaching it")
	}
}
