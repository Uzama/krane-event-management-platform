package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	adapterauthz "github.com/Uzama/krane-event-management-platform/internal/adapter/authz"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

// This exercises the real production router with the real mock OIDC issuer
// and real Postgres, same shape as events_integration_test.go and
// members_integration_test.go -- item 11 is the first real consumer of the
// /v1/events/{eventId}/rooms routes. Reuses events_integration_test.go's
// shared helpers since this file is in the same http_test package.

// newRoomsServer wires the Event and Room handlers with real dependencies
// -- room tests need to create fixture events through the real stack, then
// exercise room routes on them.
func newRoomsServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
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
	rooms := room.NewService(postgres.NewRoomRepository(pool))
	logger := discardLogger()

	router := apihttp.NewRouter(apihttp.RouterDeps{
		Event:        handler.NewEventHandler(events, logger),
		Room:         handler.NewRoomHandler(rooms, logger),
		AuthVerifier: verifier,
		Users:        users,
		Authz:        policy,
		Logger:       logger,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, pool
}

// createRoomsITEvent creates a fixture event through the real stack,
// returning its id and the creator's (auto-admin) bearer token.
func createRoomsITEvent(t *testing.T, srv *httptest.Server) (eventID, adminToken string) {
	t.Helper()

	adminSub := eventsITUniqueSubject(t)
	adminToken = mintEventsITToken(t, adminSub)

	createBody := `{"name":"Rooms IT","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating fixture event: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding fixture event: %v", err)
	}
	return created.ID, adminToken
}

// TestRoomsIntegration_AdminFullCRUD proves the full lifecycle through the
// real routes for a role the permission matrix grants full room access.
func TestRoomsIntegration_AdminFullCRUD(t *testing.T) {
	srv, _ := newRoomsServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createRoomsITEvent(t, srv)

	createBody := `{"name":"Hall A","capacity":50}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/rooms", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, "/v1/events/"+eventID+"/rooms", createBody, resp.status, resp.header, resp.body)

	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created room: %v", err)
	}
	roomPath := "/v1/events/" + eventID + "/rooms/" + created.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: roomPath, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, roomPath, "", resp.status, resp.header, resp.body)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/rooms", bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("list: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, "/v1/events/"+eventID+"/rooms", "", resp.status, resp.header, resp.body)
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &page); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	found := false
	for _, r := range page.Data {
		if r["id"] == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created room %q missing from list: %s", created.ID, resp.body)
	}

	patchBody := `{"version":1,"name":"Hall A Renamed"}`
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: roomPath, bearer: adminToken, body: patchBody})
	if resp.status != http.StatusOK {
		t.Fatalf("patch: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPatch, roomPath, patchBody, resp.status, resp.header, resp.body)
	var patched struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &patched); err != nil {
		t.Fatalf("decoding patched room: %v", err)
	}
	if patched.Name != "Hall A Renamed" || patched.Version != 2 {
		t.Fatalf("got %+v, want name=Hall A Renamed version=2", patched)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: roomPath + "?version=2", bearer: adminToken})
	if resp.status != http.StatusNoContent {
		t.Fatalf("delete: got status %d, want 204: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: roomPath, bearer: adminToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("get after delete: got status %d, want 404: %s", resp.status, resp.body)
	}
}

// TestRoomsIntegration_ContributorFullCRUD proves the role matrix's
// contributor row (room create/read/update/delete all granted) at the real
// routes, not just against role_permissions rows.
func TestRoomsIntegration_ContributorFullCRUD(t *testing.T) {
	srv, pool := newRoomsServer(t)
	eventID, _ := createRoomsITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/rooms", bearer: contributorToken, body: `{"name":"Contributor Hall"}`})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created room: %v", err)
	}
	roomPath := "/v1/events/" + eventID + "/rooms/" + created.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: roomPath, bearer: contributorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/rooms", bearer: contributorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("list: got status %d, want 200: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: roomPath, bearer: contributorToken, body: `{"version":1,"name":"Renamed by contributor"}`})
	if resp.status != http.StatusOK {
		t.Fatalf("patch: got status %d, want 200: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: roomPath + "?version=2", bearer: contributorToken})
	if resp.status != http.StatusNoContent {
		t.Fatalf("delete: got status %d, want 204: %s", resp.status, resp.body)
	}
}

// TestRoomsIntegration_Attendee_ForbiddenOnAllRoutes proves the matrix's
// attendee row (no room permission rows at all) at every one of the 5
// routes, through real requests -- not a repo/handler-level assertion.
func TestRoomsIntegration_Attendee_ForbiddenOnAllRoutes(t *testing.T) {
	srv, pool := newRoomsServer(t)
	eventID, adminToken := createRoomsITEvent(t, srv)

	// A real room to attempt read/update/delete against, created by the
	// admin -- the attendee must be forbidden regardless of whether the
	// target exists.
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/rooms", bearer: adminToken, body: `{"name":"Attendee target"}`})
	if resp.status != http.StatusCreated {
		t.Fatalf("fixture room create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding fixture room: %v", err)
	}
	roomPath := "/v1/events/" + eventID + "/rooms/" + created.ID

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/v1/events/" + eventID + "/rooms", `{"name":"Attendee room"}`},
		{"list", http.MethodGet, "/v1/events/" + eventID + "/rooms", ""},
		{"get", http.MethodGet, roomPath, ""},
		{"patch", http.MethodPatch, roomPath, `{"version":1,"name":"Attendee rename"}`},
		{"delete", http.MethodDelete, roomPath + "?version=1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doEventsITRequest(t, srv, eventsITRequest{method: tc.method, path: tc.path, bearer: attendeeToken, body: tc.body})
			if resp.status != http.StatusForbidden {
				t.Fatalf("got status %d, want 403: %s", resp.status, resp.body)
			}
		})
	}
}

// TestRoomsIntegration_CrossEventScoping_ReturnsNotFound proves the repo's
// id-AND-event_id scoping at the real routes: a room created under event A
// is unreachable via event B's path segment, even for a caller who is an
// admin of BOTH events -- so the 404 can only be the room/event_id
// mismatch, never an authz-membership 403 masquerading as one.
func TestRoomsIntegration_CrossEventScoping_ReturnsNotFound(t *testing.T) {
	srv, _ := newRoomsServer(t)
	eventA, adminToken := createRoomsITEvent(t, srv)

	// The same admin creates event B too, via the same account -- creating
	// an event auto-grants admin membership (item 08), so this admin is now
	// a real member of both events.
	createBody := `{"name":"Rooms IT event B","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating event B: got status %d, want 201: %s", resp.status, resp.body)
	}
	var eventBCreated struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &eventBCreated); err != nil {
		t.Fatalf("decoding event B: %v", err)
	}
	eventB := eventBCreated.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventA + "/rooms", bearer: adminToken, body: `{"name":"Event A room"}`})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating room in event A: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created room: %v", err)
	}

	wrongEventRoomPath := "/v1/events/" + eventB + "/rooms/" + created.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: wrongEventRoomPath, bearer: adminToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("get via wrong event: got status %d, want 404: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: wrongEventRoomPath, bearer: adminToken, body: `{"version":1,"name":"Should not apply"}`})
	if resp.status != http.StatusNotFound {
		t.Fatalf("patch via wrong event: got status %d, want 404: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: wrongEventRoomPath + "?version=1", bearer: adminToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("delete via wrong event: got status %d, want 404: %s", resp.status, resp.body)
	}

	// The room must still be reachable, untouched, via its real event.
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventA + "/rooms/" + created.ID, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get via real event: got status %d, want 200: %s", resp.status, resp.body)
	}
}

func TestRoomsIntegration_StaleVersionPatch_Returns409VersionConflict(t *testing.T) {
	srv, _ := newRoomsServer(t)
	eventID, adminToken := createRoomsITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/rooms", bearer: adminToken, body: `{"name":"Stale version room"}`})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating fixture room: got status %d, want 201: %s", resp.status, resp.body)
	}
	var rm struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &rm); err != nil {
		t.Fatalf("decoding fixture room: %v", err)
	}
	roomID := rm.ID

	body := `{"version":999,"name":"Should not apply"}`
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + eventID + "/rooms/" + roomID, bearer: adminToken, body: body})
	if resp.status != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", resp.status, resp.body)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if got.Error.Code != "version_conflict" {
		t.Fatalf("got code %q, want version_conflict", got.Error.Code)
	}
}

func TestRoomsIntegration_MalformedCursor_Returns400(t *testing.T) {
	srv, _ := newRoomsServer(t)
	eventID, adminToken := createRoomsITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/rooms?cursor=not-valid!!", bearer: adminToken})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", resp.status, resp.body)
	}
}

func TestRoomsIntegration_NoToken_Returns401(t *testing.T) {
	srv, _ := newRoomsServer(t)
	eventID, _ := createRoomsITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/rooms"})
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401: %s", resp.status, resp.body)
	}
}
