package middleware_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
)

// This file proves the real chain end to end: the real go-oidc Verifier
// against the running mock OIDC container, the real user.Service against
// krane_test, wrapping a throwaway protected handler that exists only in
// this test -- nothing here touches router.go or the OpenAPI spec. Every
// test mints its own unique subject so parallel packages never collide on
// users.subject's unique constraint (CLAUDE.md: no test may assume it's the
// only occupant of the database).

const (
	defaultOIDCIssuerURL   = "http://localhost:9090/default"
	defaultTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"
	testAudience           = "krane-api"
)

func oidcIssuerURL() string {
	if v := os.Getenv("OIDC_ISSUER_URL"); v != "" {
		return v
	}
	return defaultOIDCIssuerURL
}

func testDatabaseURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDatabaseURL
}

// realStackHandler builds the actual Auth middleware wired to the actual
// mock OIDC issuer and the actual krane_test database, wrapping a
// throwaway handler that echoes the authenticated user as JSON. Fails
// fast with guidance if either dependency isn't reachable, matching how
// the rest of the suite's integration tests behave.
func realStackHandler(t *testing.T) http.Handler {
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := middleware.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(u)
	})

	return middleware.Auth(verifier, users, logger)(echo)
}

func uniqueTestSub(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("it-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
}

// mintToken mints a token from the real mock OIDC issuer via its
// test_sub/test_aud claim-templating mapping (docker-compose.yml's
// JSON_CONFIG). aud defaults to the required audience when empty.
func mintToken(t *testing.T, subject, aud string) string {
	t.Helper()
	if aud == "" {
		aud = testAudience
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"integration-test"},
		"client_secret": {"unused"},
		"test_sub":      {subject},
		"test_aud":      {aud},
	}

	resp, err := http.Post(oidcIssuerURL()+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("posting to token endpoint: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("closing token response body: %v", err)
		}
	}()

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding token response: %v", err)
	}
	if body.AccessToken == "" {
		t.Fatalf("token endpoint returned no access_token (http status %d)", resp.StatusCode)
	}
	return body.AccessToken
}

// decodeJWTPayload base64url-decodes a compact JWT's middle segment,
// without verifying it -- test-only introspection so a test can assert
// what claims a real, live-minted token actually carries, rather than
// trusting that the endpoint we hit did what its name implies.
func decodeJWTPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d dot-separated parts, want 3 (header.payload.signature): %q", len(parts), token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("base64url-decoding token payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshaling token payload: %v", err)
	}
	return claims
}

// mintTokenFromIssuer mints from a different issuer path -- mock-oauth2-
// server's multi-issuer routing works with zero extra config (verified
// directly against the pinned image before writing this test) -- and
// asserts the returned token's own iss claim actually says so, so this
// helper can't silently degrade into re-minting from "default" (which
// would make every wrong-issuer test pass vacuously: rejected for some
// other reason, or not rejected at all if the assertion were loose).
func mintTokenFromIssuer(t *testing.T, issuerPath string) string {
	t.Helper()

	base := strings.TrimSuffix(oidcIssuerURL(), "/default")
	wantIssuer := base + "/" + issuerPath

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"integration-test-wrong-issuer"},
		"client_secret": {"unused"},
	}

	resp, err := http.Post(wantIssuer+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("posting to %s token endpoint: %v", issuerPath, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("closing token response body: %v", err)
		}
	}()

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding token response: %v", err)
	}
	if body.AccessToken == "" {
		t.Fatalf("token endpoint returned no access_token (http status %d)", resp.StatusCode)
	}

	claims := decodeJWTPayload(t, body.AccessToken)
	gotIssuer, _ := claims["iss"].(string)
	if gotIssuer != wantIssuer {
		t.Fatalf("minted token has iss=%q, want %q -- this token was NOT genuinely minted by a different issuer, the wrong-issuer test would be meaningless", gotIssuer, wantIssuer)
	}
	if gotIssuer == oidcIssuerURL() {
		t.Fatalf("minted token's issuer (%q) equals our verifier's configured issuer (%q) -- not actually a different issuer", gotIssuer, oidcIssuerURL())
	}

	return body.AccessToken
}

func doRequest(handler http.Handler, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAuthIntegration_NoToken_Returns401(t *testing.T) {
	handler := realStackHandler(t)

	rec := doRequest(handler, "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
}

func TestAuthIntegration_ValidToken_Returns200WithCorrectUser(t *testing.T) {
	handler := realStackHandler(t)
	subject := uniqueTestSub(t)

	rec := doRequest(handler, mintToken(t, subject, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var got user.User
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding echoed user: %v", err)
	}
	if got.Subject != subject {
		t.Errorf("got Subject %q, want %q", got.Subject, subject)
	}
	if got.Email != subject+"@test.krane" {
		t.Errorf("got Email %q, want %q", got.Email, subject+"@test.krane")
	}
	if got.ID == "" {
		t.Error("got empty ID; want the handler to have observed the mapped users row")
	}
}

func TestAuthIntegration_MalformedToken_Returns401NotPanic(t *testing.T) {
	handler := realStackHandler(t)

	rec := doRequest(handler, "not-a-jwt-at-all")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 (never 500) for a malformed token", rec.Code)
	}
}

// TestAuthIntegration_WrongAudience_Returns401 proves audience is the SOLE
// rejection reason at the full-stack level: the baseline request, using
// the identical minting mechanism and a fresh subject but the correct
// audience, must succeed -- otherwise a 401 on the wrong-audience request
// below wouldn't tell us audience checking is what caught it.
func TestAuthIntegration_WrongAudience_Returns401(t *testing.T) {
	handler := realStackHandler(t)

	baselineSubject := uniqueTestSub(t)
	baseline := doRequest(handler, mintToken(t, baselineSubject, testAudience))
	if baseline.Code != http.StatusOK {
		t.Fatalf("baseline request (correct audience) got status %d, want 200; body: %s -- if this fails, the wrong-audience assertion below is meaningless", baseline.Code, baseline.Body.String())
	}

	wrongAudSubject := uniqueTestSub(t)
	rec := doRequest(handler, mintToken(t, wrongAudSubject, "someone-elses-api"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 for a token minted with the wrong audience", rec.Code)
	}
}

// TestAuthIntegration_WrongIssuer_Returns401 uses mintTokenFromIssuer,
// which asserts (via decodeJWTPayload) that the token it returns was
// genuinely minted with a different iss claim before this test ever runs
// -- so a 401 here is attributable to issuer validation, not to the mint
// having silently failed or fallen back to the configured issuer.
func TestAuthIntegration_WrongIssuer_Returns401(t *testing.T) {
	handler := realStackHandler(t)

	token := mintTokenFromIssuer(t, "other")
	claims := decodeJWTPayload(t, token)
	t.Logf("minted cross-issuer token claims: %+v", claims)

	rec := doRequest(handler, token)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 for a token from a different issuer", rec.Code)
	}
}

func TestAuthIntegration_SameSubjectTwice_ReusesRowNoDuplicate(t *testing.T) {
	handler := realStackHandler(t)
	subject := uniqueTestSub(t)
	token := mintToken(t, subject, "")

	first := doRequest(handler, token)
	if first.Code != http.StatusOK {
		t.Fatalf("first request: got status %d, want 200; body: %s", first.Code, first.Body.String())
	}
	var firstUser user.User
	if err := json.Unmarshal(first.Body.Bytes(), &firstUser); err != nil {
		t.Fatalf("decoding first response: %v", err)
	}

	second := doRequest(handler, token)
	if second.Code != http.StatusOK {
		t.Fatalf("second request: got status %d, want 200; body: %s", second.Code, second.Body.String())
	}
	var secondUser user.User
	if err := json.Unmarshal(second.Body.Bytes(), &secondUser); err != nil {
		t.Fatalf("decoding second response: %v", err)
	}

	if secondUser.ID != firstUser.ID {
		t.Errorf("second sign-in got a different id (%q) than the first (%q); want the same row reused", secondUser.ID, firstUser.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testDatabaseURL())
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE subject = $1`, subject).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows for subject %q after two authenticated requests, want exactly 1", count, subject)
	}
}
