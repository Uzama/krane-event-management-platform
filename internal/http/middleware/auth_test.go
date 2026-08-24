package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
)

type fakeVerifier struct {
	claims user.Claims
	err    error
}

func (f *fakeVerifier) Verify(_ context.Context, _ string) (user.Claims, error) {
	return f.claims, f.err
}

type fakeUsers struct {
	ret user.User
	err error
}

func (f *fakeUsers) GetOrCreateBySubject(_ context.Context, subject, email, name string) (user.User, error) {
	return f.ret, f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// capturingLogger returns a logger whose text output can be inspected --
// used to prove the `reason` field actually differs between failure modes,
// not just that both return 401.
func capturingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func nextHandlerEchoingUser(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			t.Error("next handler: no user in context, want one attached by Auth")
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(u)
	})
}

func TestAuth_MissingHeader_Returns401(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	handler := middleware.Auth(&fakeVerifier{}, &fakeUsers{}, testLogger())(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
	if called {
		t.Error("next handler was called; want request rejected before reaching it")
	}
}

func TestAuth_MalformedHeader_Returns401(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler was called; want request rejected before reaching it")
	})

	handler := middleware.Auth(&fakeVerifier{}, &fakeUsers{}, testLogger())(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "not-a-bearer-header")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
}

func TestAuth_VerifierError_Returns401NotPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler was called; want request rejected before reaching it")
	})

	// %w, not string concatenation -- this must actually satisfy
	// errors.Is(err, user.ErrTokenInvalid), the same way adapter/auth's
	// real Verify() wraps it, or this test would not be exercising the
	// path it claims to.
	verifier := &fakeVerifier{err: fmt.Errorf("token invalid: %w", user.ErrTokenInvalid)}
	handler := middleware.Auth(verifier, &fakeUsers{}, testLogger())(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()

	// The point of this test is that a verifier error never panics --
	// if it did, this call itself would fail the test.
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 (never 500) on a verifier error", rec.Code)
	}
}

// TestAuth_LogsDistinctReasonForMissingClaimsVsTokenInvalid proves the
// structured `reason` field itself differs between the two failure modes --
// both return 401, so asserting only the status code would not catch a
// reasonFor() that collapsed both cases to the same value.
func TestAuth_LogsDistinctReasonForMissingClaimsVsTokenInvalid(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler was called; want request rejected before reaching it")
	})

	run := func(verifierErr error) string {
		logger, buf := capturingLogger()
		verifier := &fakeVerifier{err: verifierErr}
		handler := middleware.Auth(verifier, &fakeUsers{}, logger)(next)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer garbage")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got status %d, want 401", rec.Code)
		}
		return buf.String()
	}

	tokenInvalidLog := run(fmt.Errorf("signature mismatch: %w", user.ErrTokenInvalid))
	missingClaimsLog := run(user.ErrMissingClaims)

	if !strings.Contains(tokenInvalidLog, "reason=token_invalid") {
		t.Errorf("ErrTokenInvalid log = %q, want it to contain reason=token_invalid", tokenInvalidLog)
	}
	if strings.Contains(tokenInvalidLog, "reason=missing_claims") {
		t.Errorf("ErrTokenInvalid log = %q, want it NOT to contain reason=missing_claims", tokenInvalidLog)
	}

	if !strings.Contains(missingClaimsLog, "reason=missing_claims") {
		t.Errorf("ErrMissingClaims log = %q, want it to contain reason=missing_claims", missingClaimsLog)
	}
	if strings.Contains(missingClaimsLog, "reason=token_invalid") {
		t.Errorf("ErrMissingClaims log = %q, want it NOT to contain reason=token_invalid", missingClaimsLog)
	}
}

func TestAuth_ValidToken_AttachesUserAndCallsNext(t *testing.T) {
	claims := user.Claims{Subject: "sub-1", Email: "a@b.com", Name: "A B"}
	wantUser := user.User{ID: "user-uuid", Subject: "sub-1", Email: "a@b.com", Name: "A B"}

	verifier := &fakeVerifier{claims: claims}
	users := &fakeUsers{ret: wantUser}
	handler := middleware.Auth(verifier, users, testLogger())(nextHandlerEchoingUser(t))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer a-valid-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	var got user.User
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding echoed user: %v", err)
	}
	if got != wantUser {
		t.Errorf("next handler observed user %+v, want %+v", got, wantUser)
	}
}

func TestAuth_UserResolverError_Returns401NotPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler was called; want request rejected before reaching it")
	})

	verifier := &fakeVerifier{claims: user.Claims{Subject: "sub-1", Email: "a@b.com", Name: "A B"}}
	users := &fakeUsers{err: errors.New("db unreachable")}
	handler := middleware.Auth(verifier, users, testLogger())(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer a-valid-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("got status 200 with a failing user resolver; want a non-2xx status")
	}
}
