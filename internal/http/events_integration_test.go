package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	adapterauthz "github.com/Uzama/krane-event-management-platform/internal/adapter/authz"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
	"github.com/Uzama/krane-event-management-platform/internal/http/validator"
)

// This is the first business route exercised through the real production
// router (internal/http/router.go), the real mock OIDC issuer, and real
// Postgres all at once -- items 06/07 proved Auth/Authz in isolation
// against throwaway handlers; item 08 is the first real consumer.

const (
	eventsITOIDCIssuerURL   = "http://localhost:9090/default"
	eventsITTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"
	eventsITAudience        = "krane-api"
)

func eventsITOIDCIssuer() string {
	if v := os.Getenv("OIDC_ISSUER_URL"); v != "" {
		return v
	}
	return eventsITOIDCIssuerURL
}

func eventsITDatabaseURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return eventsITTestDatabaseURL
}

// newEventsServer boots the real, unmodified production router wired to
// real dependencies: the real go-oidc Verifier against the running mock
// OIDC container, and real adapter/postgres repositories against
// krane_test.
func newEventsServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifier, err := auth.New(ctx, eventsITOIDCIssuer(), eventsITAudience)
	if err != nil {
		t.Fatalf("auth.New against %q: %v\n\nThe suite needs the mock OIDC issuer. Run `make up` first, or `make test`, which does it for you.", eventsITOIDCIssuer(), err)
	}

	pool, err := pgxpool.New(ctx, eventsITDatabaseURL())
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
	events := event.NewService(postgres.NewEventRepository(pool))
	logger := discardLogger()

	router := apihttp.NewRouter(apihttp.RouterDeps{
		Event:        handler.NewEventHandler(events, logger),
		AuthVerifier: verifier,
		Users:        users,
		Authz:        policy,
		Logger:       logger,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, pool
}

func eventsITUniqueSubject(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("events-it-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
}

// mintEventsITToken mints a token from the real mock OIDC issuer via its
// test_sub/test_aud claim-templating mapping (docker-compose.yml's
// JSON_CONFIG), same mechanism as the item 06/07 integration tests.
func mintEventsITToken(t *testing.T, subject string) string {
	t.Helper()

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"integration-test"},
		"client_secret": {"unused"},
		"test_sub":      {subject},
		"test_aud":      {eventsITAudience},
	}

	resp, err := http.Post(eventsITOIDCIssuer()+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
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

func seedEventsITMember(t *testing.T, pool *pgxpool.Pool, eventID, subject, role string) {
	t.Helper()

	users := user.NewService(postgres.NewUserRepository(pool))
	u, err := users.GetOrCreateBySubject(context.Background(), subject, subject+"@test.krane", "Events IT User")
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
}

type eventsITRequest struct {
	method         string
	path           string
	bearer         string
	body           string // empty means no body
	idempotencyKey string // item 21: set as the Idempotency-Key header when non-empty
}

type eventsITResponse struct {
	status int
	header http.Header
	body   []byte
}

// doEventsITRequest performs the request and reads/closes the body itself
// -- the response body is closed here, in the one function that opens it,
// rather than handed back open for callers to remember to close.
func doEventsITRequest(t *testing.T, srv *httptest.Server, r eventsITRequest) eventsITResponse {
	t.Helper()

	var bodyReader io.Reader
	if r.body != "" {
		bodyReader = strings.NewReader(r.body)
	}
	req, err := http.NewRequest(r.method, srv.URL+r.path, bodyReader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.idempotencyKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("performing request: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("closing response body: %v", err)
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	return eventsITResponse{status: resp.StatusCode, header: resp.Header, body: data}
}

func loadEventsITSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := validator.LoadSpec()
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	return spec
}

// assertSpec re-issues a fresh, unconsumed request purely for validation
// (matching TestRouter_Health_ResponseMatchesSpec's pattern) and checks the
// LIVE recorded status/headers/body against openapi/openapi.yaml.
func assertSpec(t *testing.T, spec *openapi3.T, method, path, reqBody string, status int, header http.Header, respBody []byte) {
	t.Helper()

	var bodyReader io.Reader
	if reqBody != "" {
		bodyReader = bytes.NewReader([]byte(reqBody))
	}
	validationReq := httptest.NewRequest(method, path, bodyReader)
	if reqBody != "" {
		validationReq.Header.Set("Content-Type", "application/json")
	}

	if err := validator.ValidateRequest(context.Background(), spec, validationReq); err != nil {
		t.Errorf("ValidateRequest(%s %s): %v", method, path, err)
	}
	if err := validator.ValidateResponse(context.Background(), spec, validationReq, status, header, respBody); err != nil {
		t.Errorf("ValidateResponse(%s %s, status %d): %v\nbody: %s", method, path, status, err, respBody)
	}
}

// assertSpecWithIdempotencyKey is assertSpec plus the Idempotency-Key
// header (item 21) -- POST .../invitations/bulk declares it required, so
// the validation-only request assertSpec builds needs it set too, or
// kin-openapi rejects the request itself before it ever checks the body.
func assertSpecWithIdempotencyKey(t *testing.T, spec *openapi3.T, method, path, reqBody, idempotencyKey string, status int, header http.Header, respBody []byte) {
	t.Helper()

	var bodyReader io.Reader
	if reqBody != "" {
		bodyReader = bytes.NewReader([]byte(reqBody))
	}
	validationReq := httptest.NewRequest(method, path, bodyReader)
	if reqBody != "" {
		validationReq.Header.Set("Content-Type", "application/json")
	}
	validationReq.Header.Set("Idempotency-Key", idempotencyKey)

	if err := validator.ValidateRequest(context.Background(), spec, validationReq); err != nil {
		t.Errorf("ValidateRequest(%s %s): %v", method, path, err)
	}
	if err := validator.ValidateResponse(context.Background(), spec, validationReq, status, header, respBody); err != nil {
		t.Errorf("ValidateResponse(%s %s, status %d): %v\nbody: %s", method, path, status, err, respBody)
	}
}

func TestEventsIntegration_FullCRUDHappyPath(t *testing.T) {
	srv, _ := newEventsServer(t)
	spec := loadEventsITSpec(t)

	creatorSub := eventsITUniqueSubject(t)
	creatorToken := mintEventsITToken(t, creatorSub)

	// --- Create ---
	createBody := `{"name":"IT Conf","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: creatorToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, "/v1/events", createBody, resp.status, resp.header, resp.body)

	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create response has empty id")
	}

	// --- Get ---
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: creatorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, "/v1/events/"+created.ID, "", resp.status, resp.header, resp.body)

	// --- List: the creator's new event must appear ---
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events", bearer: creatorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("list: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, "/v1/events", "", resp.status, resp.header, resp.body)

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &list); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	found := false
	for _, e := range list.Data {
		if e.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("list does not contain the created event %q: %s", created.ID, resp.body)
	}

	// --- Patch ---
	patchBody := fmt.Sprintf(`{"version":%d,"name":"Renamed IT Conf"}`, created.Version)
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + created.ID, bearer: creatorToken, body: patchBody})
	if resp.status != http.StatusOK {
		t.Fatalf("patch: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPatch, "/v1/events/"+created.ID, patchBody, resp.status, resp.header, resp.body)

	var patched struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &patched); err != nil {
		t.Fatalf("decoding patch response: %v", err)
	}
	if patched.Name != "Renamed IT Conf" {
		t.Fatalf("got name %q, want Renamed IT Conf", patched.Name)
	}

	// --- Delete ---
	deletePath := fmt.Sprintf("/v1/events/%s?version=%d", created.ID, patched.Version)
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: deletePath, bearer: creatorToken})
	if resp.status != http.StatusNoContent {
		t.Fatalf("delete: got status %d, want 204: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodDelete, deletePath, "", resp.status, resp.header, resp.body)

	// --- Get after delete: 404, the creator IS still a member, so this
	// must not be the authz 403 non-leak path -- it's a genuine not-found.
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: creatorToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("get after delete: got status %d, want 404: %s", resp.status, resp.body)
	}
}

func TestEventsIntegration_Contributor_ForbiddenFromUpdateAndDelete(t *testing.T) {
	srv, pool := newEventsServer(t)

	adminSub := eventsITUniqueSubject(t)
	adminToken := mintEventsITToken(t, adminSub)

	createBody := `{"name":"Contributor Test","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, created.ID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	// Contributor CAN read.
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: contributorToken})
	if resp.status != http.StatusOK {
		t.Errorf("contributor get: got status %d, want 200", resp.status)
	}

	// Contributor CANNOT update.
	patchBody := fmt.Sprintf(`{"version":%d,"name":"Should not apply"}`, created.Version)
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + created.ID, bearer: contributorToken, body: patchBody})
	if resp.status != http.StatusForbidden {
		t.Errorf("contributor patch: got status %d, want 403", resp.status)
	}

	// Contributor CANNOT delete.
	resp = doEventsITRequest(t, srv, eventsITRequest{
		method: http.MethodDelete,
		path:   fmt.Sprintf("/v1/events/%s?version=%d", created.ID, created.Version),
		bearer: contributorToken,
	})
	if resp.status != http.StatusForbidden {
		t.Errorf("contributor delete: got status %d, want 403", resp.status)
	}
}

func TestEventsIntegration_Attendee_CanReadButForbiddenFromMutation(t *testing.T) {
	srv, pool := newEventsServer(t)

	adminSub := eventsITUniqueSubject(t)
	adminToken := mintEventsITToken(t, adminSub)

	createBody := `{"name":"Attendee Test","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: adminToken, body: createBody})
	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, created.ID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: attendeeToken})
	if resp.status != http.StatusOK {
		t.Errorf("attendee get: got status %d, want 200", resp.status)
	}

	patchBody := fmt.Sprintf(`{"version":%d,"name":"Should not apply"}`, created.Version)
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + created.ID, bearer: attendeeToken, body: patchBody})
	if resp.status != http.StatusForbidden {
		t.Errorf("attendee patch: got status %d, want 403", resp.status)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{
		method: http.MethodDelete,
		path:   fmt.Sprintf("/v1/events/%s?version=%d", created.ID, created.Version),
		bearer: attendeeToken,
	})
	if resp.status != http.StatusForbidden {
		t.Errorf("attendee delete: got status %d, want 403", resp.status)
	}
}

// TestEventsIntegration_NonMember_Returns403NotFound is item 07's non-leak
// rule, proved again at the full events route: a real event a caller has
// no standing on must answer identically to a non-existent one.
func TestEventsIntegration_NonMember_Returns403NotFound(t *testing.T) {
	srv, _ := newEventsServer(t)

	ownerSub := eventsITUniqueSubject(t)
	ownerToken := mintEventsITToken(t, ownerSub)

	createBody := `{"name":"Not yours","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: ownerToken, body: createBody})
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	strangerSub := eventsITUniqueSubject(t)
	strangerToken := mintEventsITToken(t, strangerSub)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: strangerToken})
	if resp.status != http.StatusForbidden {
		t.Fatalf("got status %d, want 403 (never 404) for a real event the caller isn't a member of: %s", resp.status, resp.body)
	}
}

// TestEventsIntegration_NonMember_SoftDeletedEvent_Returns403NotNotFound
// proves the 403/404 split is not an existence oracle in either direction:
// a soft-deleted event is 404 ONLY to someone who is already a member (the
// owner's own path through TestEventsIntegration_FullCRUDHappyPath's
// "Get after delete" step). Someone with NO event_members row at all must
// still get the identical 403 a non-existent event would give them --
// Authz's membership check runs before the handler ever learns the row is
// deleted, and event_members carries no deleted_at of its own to leak.
func TestEventsIntegration_NonMember_SoftDeletedEvent_Returns403NotFound(t *testing.T) {
	srv, _ := newEventsServer(t)

	ownerSub := eventsITUniqueSubject(t)
	ownerToken := mintEventsITToken(t, ownerSub)

	createBody := `{"name":"Deleted and not yours","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: ownerToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	// The owner soft-deletes it -- confirmed 204, so what follows is
	// genuinely testing against a deleted row, not a live one.
	resp = doEventsITRequest(t, srv, eventsITRequest{
		method: http.MethodDelete,
		path:   fmt.Sprintf("/v1/events/%s?version=%d", created.ID, created.Version),
		bearer: ownerToken,
	})
	if resp.status != http.StatusNoContent {
		t.Fatalf("owner delete: got status %d, want 204: %s", resp.status, resp.body)
	}

	// A stranger with no event_members row on this event at all -- not the
	// owner, never invited -- must get 403, never 404.
	strangerSub := eventsITUniqueSubject(t)
	strangerToken := mintEventsITToken(t, strangerSub)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: strangerToken})
	if resp.status != http.StatusForbidden {
		t.Fatalf("non-member on a soft-deleted event: got status %d, want 403 (never 404): %s", resp.status, resp.body)
	}
}

// TestEventsIntegration_NoToken_Returns401 is the security boundary the
// validator's NoopAuthenticationFunc does NOT cover: this hits the real,
// production apihttp.NewRouter over real HTTP with no Authorization header
// at all, so the 401 comes from the real Auth middleware's real JWKS/token
// check, not from anything in internal/http/validator. The validator is
// never involved in this test.
func TestEventsIntegration_NoToken_Returns401(t *testing.T) {
	srv, _ := newEventsServer(t)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events"})
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", resp.status)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", body: `{}`})
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("create with no token: got status %d, want 401", resp.status)
	}
}

// TestEventsIntegration_AttendeeEventRead_NoEmailOrRosterKeyAnywhere is item
// 10's core deliverable (FEATURES.md), proved at the real, full stack: an
// attendee reading an event they belong to must get back a body with no
// roster and no email anywhere. The event is populated first -- an admin
// (the creator), a contributor, and the attendee under test -- so this is
// proven against a real roster with real emails behind it, not vacuously on
// an empty one. EventResponse has no roster/email field at all today, so
// this is a regression guard as much as a proof: it fails the moment anyone
// adds one without a presenter to match.
func TestEventsIntegration_AttendeeEventRead_NoEmailOrRosterKeyAnywhere(t *testing.T) {
	srv, pool := newEventsServer(t)

	adminSub := eventsITUniqueSubject(t)
	adminToken := mintEventsITToken(t, adminSub)

	createBody := `{"name":"Populated Event","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, created.ID, contributorSub, "contributor")

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, created.ID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + created.ID, bearer: attendeeToken})
	if resp.status != http.StatusOK {
		t.Fatalf("attendee get: got status %d, want 200: %s", resp.status, resp.body)
	}
	for _, forbidden := range []string{"email", "roster", "members"} {
		assertNoKeyContaining(t, resp.body, forbidden)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events", bearer: attendeeToken})
	if resp.status != http.StatusOK {
		t.Fatalf("attendee list: got status %d, want 200: %s", resp.status, resp.body)
	}
	for _, forbidden := range []string{"email", "roster", "members"} {
		assertNoKeyContaining(t, resp.body, forbidden)
	}
}

// TestEventsIntegration_AttendeeEventList_OnlyOwnEvents is item 10's must:
// test (b) event half -- session-list scoping is deferred to item 12
// (FEATURES.md forward note; no session-list endpoint exists yet). Event
// scoping itself already ships from EventRepository.List's event_members
// join (item 08); this re-proves it explicitly under item 10's own name,
// deliberately light rather than rebuilding item 08's suite.
func TestEventsIntegration_AttendeeEventList_OnlyOwnEvents(t *testing.T) {
	srv, pool := newEventsServer(t)

	attendeeSub := eventsITUniqueSubject(t)
	attendeeToken := mintEventsITToken(t, attendeeSub)

	// An event the attendee belongs to.
	ownerASub := eventsITUniqueSubject(t)
	ownerAToken := mintEventsITToken(t, ownerASub)
	bodyA := `{"name":"Attendee's Event","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: ownerAToken, body: bodyA})
	if resp.status != http.StatusCreated {
		t.Fatalf("create event A: got status %d, want 201: %s", resp.status, resp.body)
	}
	var eventA struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &eventA); err != nil {
		t.Fatalf("decoding event A: %v", err)
	}
	seedEventsITMember(t, pool, eventA.ID, attendeeSub, "attendee")

	// A second, unrelated event with a different admin -- the attendee has
	// no standing on it at all.
	ownerBSub := eventsITUniqueSubject(t)
	ownerBToken := mintEventsITToken(t, ownerBSub)
	bodyB := `{"name":"Someone Else's Event","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: ownerBToken, body: bodyB})
	if resp.status != http.StatusCreated {
		t.Fatalf("create event B: got status %d, want 201: %s", resp.status, resp.body)
	}
	var eventB struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &eventB); err != nil {
		t.Fatalf("decoding event B: %v", err)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events", bearer: attendeeToken})
	if resp.status != http.StatusOK {
		t.Fatalf("attendee list: got status %d, want 200: %s", resp.status, resp.body)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &list); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != eventA.ID {
		t.Fatalf("attendee's event list = %+v, want exactly [%s]", list.Data, eventA.ID)
	}
}

// assertNoKeyContaining walks the full JSON tree -- maps AND slices, at
// every nesting depth -- and fails if any object key contains substr,
// case-insensitively. Deliberately KEY-only: a value smuggled under a
// differently-named key would not be caught -- an accepted limit for this
// feature, not a general PII scanner. Mirrors
// internal/http/response.assertNoKeyContaining; duplicated rather than
// imported since that one lives in an internal _test.go file in a
// different package.
func assertNoKeyContaining(t *testing.T, body []byte, substr string) {
	t.Helper()

	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	walkNoKeyContaining(t, tree, substr, body)
}

func walkNoKeyContaining(t *testing.T, node any, substr string, body []byte) {
	t.Helper()

	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if strings.Contains(strings.ToLower(k), strings.ToLower(substr)) {
				t.Errorf("found key %q containing %q in response body: %s", k, substr, body)
			}
			walkNoKeyContaining(t, child, substr, body)
		}
	case []any:
		for _, child := range v {
			walkNoKeyContaining(t, child, substr, body)
		}
	}
}

func TestEventsIntegration_List_MalformedCursor_Returns400(t *testing.T) {
	srv, _ := newEventsServer(t)

	subject := eventsITUniqueSubject(t)
	token := mintEventsITToken(t, subject)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events?cursor=not-a-real-cursor!!", bearer: token})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", resp.status, resp.body)
	}
}
