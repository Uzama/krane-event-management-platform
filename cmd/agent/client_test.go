package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	adapterauthz "github.com/Uzama/krane-event-management-platform/internal/adapter/authz"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

// client_test.go exercises Client against the real production router (same
// shape as internal/http/*_integration_test.go): real mock OIDC issuer,
// real Postgres, real Authz chokepoint. The agent's own API client must
// never see anything the caller's token wouldn't -- these tests are the
// proof it inherits the token's authz rather than working around it.

const (
	agentITOIDCIssuerURL   = "http://localhost:9090/default"
	agentITTestDatabaseURL = "postgres://krane_app:dev_only_app@localhost:5432/krane_test?sslmode=disable"
	agentITAudience        = "krane-api"
)

func agentITOIDCIssuer() string {
	if v := os.Getenv("OIDC_ISSUER_URL"); v != "" {
		return v
	}
	return agentITOIDCIssuerURL
}

func agentITDatabaseURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return agentITTestDatabaseURL
}

func newAgentITServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifier, err := auth.New(ctx, agentITOIDCIssuer(), agentITAudience)
	if err != nil {
		t.Fatalf("auth.New against %q: %v\n\nThe suite needs the mock OIDC issuer. Run `make up` first, or `make test`, which does it for you.", agentITOIDCIssuer(), err)
	}

	pool, err := pgxpool.New(ctx, agentITDatabaseURL())
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
	rooms := room.NewService(postgres.NewRoomRepository(pool))
	sessions := session.NewService(postgres.NewSessionRepository(pool))
	logger := discardLogger()

	router := apihttp.NewRouter(apihttp.RouterDeps{
		Event:        handler.NewEventHandler(events, logger),
		Room:         handler.NewRoomHandler(rooms, logger),
		Session:      handler.NewSessionHandler(sessions, events, logger),
		AuthVerifier: verifier,
		Users:        users,
		Authz:        policy,
		Logger:       logger,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, pool
}

func agentITUniqueSubject(t *testing.T, tag string) string {
	t.Helper()
	return fmt.Sprintf("agent-it-%s-%s-%d", tag, strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
}

func mintAgentITToken(t *testing.T, subject string) string {
	t.Helper()

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"integration-test"},
		"client_secret": {"unused"},
		"test_sub":      {subject},
		"test_aud":      {agentITAudience},
	}

	resp, err := http.Post(agentITOIDCIssuer()+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
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

// createAgentITEvent creates a fixture event as a fresh admin subject and
// returns its id alongside that admin's bearer token.
func createAgentITEvent(t *testing.T, srv *httptest.Server) (eventID, adminToken string) {
	t.Helper()

	adminToken = mintAgentITToken(t, agentITUniqueSubject(t, "admin"))
	body := `{"name":"Agent IT Event","timezone":"America/New_York","starts_at":"2026-09-01T09:00:00Z","ends_at":"2026-09-01T17:00:00Z"}`

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/events", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("creating fixture event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating fixture event: status %d", resp.StatusCode)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding fixture event: %v", err)
	}
	return created.ID, adminToken
}

func createAgentITRoom(t *testing.T, srv *httptest.Server, eventID, adminToken string) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/events/"+eventID+"/rooms", strings.NewReader(`{"name":"Main Hall","capacity":50}`))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("creating fixture room: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating fixture room: status %d", resp.StatusCode)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding fixture room: %v", err)
	}
	return created.ID
}

func createAgentITSession(t *testing.T, srv *httptest.Server, pool *pgxpool.Pool, eventID, roomID, adminToken string) string {
	t.Helper()

	speakerSubject := agentITUniqueSubject(t, "speaker")
	users := user.NewService(postgres.NewUserRepository(pool))
	speaker, err := users.GetOrCreateBySubject(context.Background(), speakerSubject, speakerSubject+"@test.krane", "Agent IT Speaker")
	if err != nil {
		t.Fatalf("creating speaker: %v", err)
	}

	body := fmt.Sprintf(`{"room_id":%q,"speaker_id":%q,"title":"Keynote","starts_at":"2026-09-01T10:00:00","ends_at":"2026-09-01T11:00:00"}`, roomID, speaker.ID)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/events/"+eventID+"/sessions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("creating fixture session: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating fixture session: status %d", resp.StatusCode)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding fixture session: %v", err)
	}
	return created.ID
}

func TestClientIntegration_ListEvents_ScopedToCallersMembership(t *testing.T) {
	srv, _ := newAgentITServer(t)
	eventID, adminToken := createAgentITEvent(t, srv)

	c := NewClient(srv.URL, adminToken)
	list, err := c.ListEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != eventID {
		t.Fatalf("expected exactly the fixture event %q, got %+v", eventID, list.Data)
	}
}

func TestClientIntegration_GetEvent_ReturnsFields(t *testing.T) {
	srv, _ := newAgentITServer(t)
	eventID, adminToken := createAgentITEvent(t, srv)

	c := NewClient(srv.URL, adminToken)
	got, err := c.GetEvent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.ID != eventID || got.Name != "Agent IT Event" || got.Timezone != "America/New_York" {
		t.Fatalf("unexpected event: %+v", got)
	}
}

func TestClientIntegration_ListRoomsAndSessions_ReturnFixtures(t *testing.T) {
	srv, pool := newAgentITServer(t)
	eventID, adminToken := createAgentITEvent(t, srv)
	roomID := createAgentITRoom(t, srv, eventID, adminToken)
	sessionID := createAgentITSession(t, srv, pool, eventID, roomID, adminToken)

	c := NewClient(srv.URL, adminToken)

	rooms, err := c.ListRooms(context.Background(), eventID, "")
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms.Data) != 1 || rooms.Data[0].ID != roomID {
		t.Fatalf("expected exactly the fixture room %q, got %+v", roomID, rooms.Data)
	}

	sessions, err := c.ListSessions(context.Background(), eventID, "")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions.Data) != 1 || sessions.Data[0].ID != sessionID {
		t.Fatalf("expected exactly the fixture session %q, got %+v", sessionID, sessions.Data)
	}
}

// TestClientIntegration_NonMember_SurfacesForbidden_NeverRetriesOrElevates
// proves the agent's client does exactly what the API says and nothing
// more: a caller who isn't a member of the event gets the same 403 a human
// would, wrapped as a Go error the tool layer can report -- never silently
// retried, never swallowed into an empty result.
func TestClientIntegration_NonMember_SurfacesForbidden_NeverRetriesOrElevates(t *testing.T) {
	srv, _ := newAgentITServer(t)
	eventID, _ := createAgentITEvent(t, srv)

	outsiderToken := mintAgentITToken(t, agentITUniqueSubject(t, "outsider"))
	c := NewClient(srv.URL, outsiderToken)

	_, err := c.GetEvent(context.Background(), eventID)
	if err == nil {
		t.Fatalf("expected an error for a non-member GetEvent, got none")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (code=%s)", apiErr.Status, apiErr.Code)
	}
}
