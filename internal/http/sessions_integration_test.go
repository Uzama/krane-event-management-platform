package http_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

// This exercises the real production router with the real mock OIDC issuer
// and real Postgres, same shape as rooms_integration_test.go -- item 12 is
// the first real consumer of the /v1/events/{eventId}/sessions routes.
// Reuses events_integration_test.go's shared helpers since this file is in
// the same http_test package.

// newSessionsServer wires the Event, Room, and Session handlers with real
// dependencies -- session tests need to create fixture events and rooms
// through the real stack, then exercise session routes on them.
func newSessionsServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
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

// createSessionsITEvent creates a fixture event with the given IANA
// timezone through the real stack, spanning all of 2026 so both of the
// year's DST transitions fall inside it. Returns its id and the creator's
// (auto-admin) bearer token.
func createSessionsITEvent(t *testing.T, srv *httptest.Server, timezone string) (eventID, adminToken string) {
	t.Helper()

	adminSub := eventsITUniqueSubject(t)
	adminToken = mintEventsITToken(t, adminSub)

	createBody := fmt.Sprintf(`{"name":"Sessions IT","timezone":%q,"starts_at":"2026-01-01T00:00:00Z","ends_at":"2026-12-31T23:59:59Z"}`, timezone)
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

// createSessionsITRoom creates a fixture room in eventID through the real
// stack, returning its id.
func createSessionsITRoom(t *testing.T, srv *httptest.Server, eventID, adminToken string) string {
	t.Helper()

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/rooms", bearer: adminToken, body: `{"name":"Hall A"}`})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating fixture room: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding fixture room: %v", err)
	}
	return created.ID
}

// createSessionsITSpeaker inserts a plain user directly -- a speaker needs
// no event_members row (docs/requirements.md D3: sessions.speaker_id
// references any user).
func createSessionsITSpeaker(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	subject := eventsITUniqueSubject(t)
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (subject, email, name) VALUES ($1, $2, $3) RETURNING id::text`,
		subject, subject+"@test.krane", "Speaker",
	).Scan(&id)
	if err != nil {
		t.Fatalf("inserting speaker user: %v", err)
	}
	return id
}

func sessionCreateBody(roomID, speakerID, title, startsAt, endsAt string) string {
	return fmt.Sprintf(`{"room_id":%q,"speaker_id":%q,"title":%q,"starts_at":%q,"ends_at":%q}`,
		roomID, speakerID, title, startsAt, endsAt)
}

// TestSessionsIntegration_AdminFullCRUD proves the full lifecycle through
// the real routes for a role the permission matrix grants full session
// access.
func TestSessionsIntegration_AdminFullCRUD(t *testing.T) {
	srv, pool := newSessionsServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	createBody := sessionCreateBody(roomID, speakerID, "Keynote", "2026-06-15T09:00:00", "2026-06-15T10:00:00")
	sessionsPath := "/v1/events/" + eventID + "/sessions"
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: sessionsPath, bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, sessionsPath, createBody, resp.status, resp.header, resp.body)

	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created session: %v", err)
	}
	sessionPath := sessionsPath + "/" + created.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: sessionPath, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, sessionPath, "", resp.status, resp.header, resp.body)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: sessionsPath, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("list: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, sessionsPath, "", resp.status, resp.header, resp.body)
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &page); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	found := false
	for _, s := range page.Data {
		if s["id"] == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created session %q missing from list: %s", created.ID, resp.body)
	}

	patchBody := `{"version":1,"title":"Keynote Renamed"}`
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: sessionPath, bearer: adminToken, body: patchBody})
	if resp.status != http.StatusOK {
		t.Fatalf("patch: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPatch, sessionPath, patchBody, resp.status, resp.header, resp.body)
	var patched struct {
		Title   string `json:"title"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &patched); err != nil {
		t.Fatalf("decoding patched session: %v", err)
	}
	if patched.Title != "Keynote Renamed" || patched.Version != 2 {
		t.Fatalf("got %+v, want title=Keynote Renamed version=2", patched)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: sessionPath + "?version=2", bearer: adminToken})
	if resp.status != http.StatusNoContent {
		t.Fatalf("delete: got status %d, want 204: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: sessionPath, bearer: adminToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("get after delete: got status %d, want 404: %s", resp.status, resp.body)
	}
}

// TestSessionsIntegration_ContributorFullCRUD proves the role matrix's
// contributor row (session create/read/update/delete all granted) at the
// real routes.
func TestSessionsIntegration_ContributorFullCRUD(t *testing.T) {
	srv, pool := newSessionsServer(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	sessionsPath := "/v1/events/" + eventID + "/sessions"
	createBody := sessionCreateBody(roomID, speakerID, "Contributor session", "2026-06-15T09:00:00", "2026-06-15T10:00:00")
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: sessionsPath, bearer: contributorToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created session: %v", err)
	}
	sessionPath := sessionsPath + "/" + created.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: sessionPath, bearer: contributorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: sessionPath, bearer: contributorToken, body: `{"version":1,"title":"Renamed by contributor"}`})
	if resp.status != http.StatusOK {
		t.Fatalf("patch: got status %d, want 200: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: sessionPath + "?version=2", bearer: contributorToken})
	if resp.status != http.StatusNoContent {
		t.Fatalf("delete: got status %d, want 204: %s", resp.status, resp.body)
	}
}

// TestSessionsIntegration_Attendee_ReadOnly proves the matrix's attendee
// row: session:read is granted (unlike rooms, where attendee has zero
// permission rows), but create/update/delete are not.
func TestSessionsIntegration_Attendee_ReadOnly(t *testing.T) {
	srv, pool := newSessionsServer(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	sessionsPath := "/v1/events/" + eventID + "/sessions"
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: sessionsPath, bearer: adminToken, body: sessionCreateBody(roomID, speakerID, "Attendee target", "2026-06-15T09:00:00", "2026-06-15T10:00:00")})
	if resp.status != http.StatusCreated {
		t.Fatalf("fixture session create: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding fixture session: %v", err)
	}
	sessionPath := sessionsPath + "/" + created.ID

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	readCases := []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, sessionsPath},
		{"get", http.MethodGet, sessionPath},
	}
	for _, tc := range readCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doEventsITRequest(t, srv, eventsITRequest{method: tc.method, path: tc.path, bearer: attendeeToken})
			if resp.status != http.StatusOK {
				t.Fatalf("got status %d, want 200: %s", resp.status, resp.body)
			}
		})
	}

	writeCases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, sessionsPath, sessionCreateBody(roomID, speakerID, "Attendee session", "2026-06-15T11:00:00", "2026-06-15T12:00:00")},
		{"patch", http.MethodPatch, sessionPath, `{"version":1,"title":"Attendee rename"}`},
		{"delete", http.MethodDelete, sessionPath + "?version=1", ""},
	}
	for _, tc := range writeCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doEventsITRequest(t, srv, eventsITRequest{method: tc.method, path: tc.path, bearer: attendeeToken, body: tc.body})
			if resp.status != http.StatusForbidden {
				t.Fatalf("got status %d, want 403: %s", resp.status, resp.body)
			}
		})
	}
}

// TestSessionsIntegration_AttendeeSessionList_OnlyForEventsTheyBelongTo
// discharges item 10's deferred forward-note (FEATURES.md item 12,
// AUDIT.md's item 10 entry): no session-list endpoint existed at item 10,
// so its own must (b) test was written against events only. Sessions are
// always requested under one /v1/events/{eventId}/sessions path, so
// "scoping" here is entirely the Authz chokepoint: an attendee gets 403
// listing sessions of an event they don't belong to, and 200 with the
// event's full list once they do -- there is no per-session field to
// filter within one event, unlike the roster's email visibility.
func TestSessionsIntegration_AttendeeSessionList_OnlyForEventsTheyBelongTo(t *testing.T) {
	srv, pool := newSessionsServer(t)
	memberEventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, memberEventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + memberEventID + "/sessions", bearer: adminToken, body: sessionCreateBody(roomID, speakerID, "Member event session", "2026-06-15T09:00:00", "2026-06-15T10:00:00")})
	if resp.status != http.StatusCreated {
		t.Fatalf("fixture session create: got status %d, want 201: %s", resp.status, resp.body)
	}

	otherEventID, _ := createSessionsITEvent(t, srv, "Asia/Colombo")

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, memberEventID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + otherEventID + "/sessions", bearer: attendeeToken})
	if resp.status != http.StatusForbidden {
		t.Fatalf("list on event attendee doesn't belong to: got status %d, want 403: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + memberEventID + "/sessions", bearer: attendeeToken})
	if resp.status != http.StatusOK {
		t.Fatalf("list on event attendee belongs to: got status %d, want 200: %s", resp.status, resp.body)
	}
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &page); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("got %d sessions, want exactly the 1 in this event: %s", len(page.Data), resp.body)
	}
}

// TestSessionsIntegration_CrossEventScoping_ReturnsNotFound proves the
// repo's id-AND-event_id scoping at the real routes: a session created
// under event A is unreachable via event B's path segment, even for a
// caller who is an admin of both events.
func TestSessionsIntegration_CrossEventScoping_ReturnsNotFound(t *testing.T) {
	srv, pool := newSessionsServer(t)
	eventA, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, eventA, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventA + "/sessions", bearer: adminToken, body: sessionCreateBody(roomID, speakerID, "Event A session", "2026-06-15T09:00:00", "2026-06-15T10:00:00")})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating session in event A: got status %d, want 201: %s", resp.status, resp.body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created session: %v", err)
	}

	// Create event B through the SAME admin account (mirrors
	// rooms_integration_test.go), so this admin is a real member of both.
	createBody := `{"name":"Sessions IT event B","timezone":"Asia/Colombo","starts_at":"2026-01-01T00:00:00Z","ends_at":"2026-12-31T23:59:59Z"}`
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating event B: got status %d, want 201: %s", resp.status, resp.body)
	}
	var eventBCreated struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &eventBCreated); err != nil {
		t.Fatalf("decoding event B: %v", err)
	}

	wrongEventSessionPath := "/v1/events/" + eventBCreated.ID + "/sessions/" + created.ID

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: wrongEventSessionPath, bearer: adminToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("get via wrong event: got status %d, want 404: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: wrongEventSessionPath, bearer: adminToken, body: `{"version":1,"title":"Should not apply"}`})
	if resp.status != http.StatusNotFound {
		t.Fatalf("patch via wrong event: got status %d, want 404: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: wrongEventSessionPath + "?version=1", bearer: adminToken})
	if resp.status != http.StatusNotFound {
		t.Fatalf("delete via wrong event: got status %d, want 404: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventA + "/sessions/" + created.ID, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get via real event: got status %d, want 200: %s", resp.status, resp.body)
	}
}

func TestSessionsIntegration_InvalidRoom_Returns404(t *testing.T) {
	srv, pool := newSessionsServer(t)
	eventA, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	speakerID := createSessionsITSpeaker(t, pool)

	t.Run("missing room", func(t *testing.T) {
		body := sessionCreateBody("01900000-0000-7000-8000-000000000000", speakerID, "No room", "2026-06-15T09:00:00", "2026-06-15T10:00:00")
		resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventA + "/sessions", bearer: adminToken, body: body})
		if resp.status != http.StatusNotFound {
			t.Fatalf("got status %d, want 404: %s", resp.status, resp.body)
		}
		var got struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(resp.body, &got); err != nil {
			t.Fatalf("decoding error: %v", err)
		}
		if got.Error.Code != "room_not_found" {
			t.Fatalf("got code %q, want room_not_found", got.Error.Code)
		}
	})

	t.Run("room in different event", func(t *testing.T) {
		eventB, eventBAdminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
		roomInB := createSessionsITRoom(t, srv, eventB, eventBAdminToken)

		body := sessionCreateBody(roomInB, speakerID, "Cross-event room", "2026-06-15T09:00:00", "2026-06-15T10:00:00")
		resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventA + "/sessions", bearer: adminToken, body: body})
		if resp.status != http.StatusNotFound {
			t.Fatalf("got status %d, want 404: %s", resp.status, resp.body)
		}
	})
}

func TestSessionsIntegration_InvalidSpeaker_Returns404(t *testing.T) {
	srv, _ := newSessionsServer(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)

	body := sessionCreateBody(roomID, "01900000-0000-7000-8000-000000000000", "No speaker", "2026-06-15T09:00:00", "2026-06-15T10:00:00")
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/sessions", bearer: adminToken, body: body})
	if resp.status != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", resp.status, resp.body)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding error: %v", err)
	}
	if got.Error.Code != "speaker_not_found" {
		t.Fatalf("got code %q, want speaker_not_found", got.Error.Code)
	}
}

func TestSessionsIntegration_NonexistentLocalTime_Returns422(t *testing.T) {
	srv, pool := newSessionsServer(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "America/New_York")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	// 2026-03-08: America/New_York jumps 2:00am -> 3:00am. 2:30am never happens.
	body := sessionCreateBody(roomID, speakerID, "Gap time", "2026-03-08T02:30:00", "2026-03-08T04:00:00")
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/sessions", bearer: adminToken, body: body})
	if resp.status != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", resp.status, resp.body)
	}
}

func TestSessionsIntegration_StaleVersionPatch_Returns409VersionConflict(t *testing.T) {
	srv, pool := newSessionsServer(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/sessions", bearer: adminToken, body: sessionCreateBody(roomID, speakerID, "Stale version session", "2026-06-15T09:00:00", "2026-06-15T10:00:00")})
	if resp.status != http.StatusCreated {
		t.Fatalf("creating fixture session: got status %d, want 201: %s", resp.status, resp.body)
	}
	var s struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &s); err != nil {
		t.Fatalf("decoding fixture session: %v", err)
	}

	body := `{"version":999,"title":"Should not apply"}`
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + eventID + "/sessions/" + s.ID, bearer: adminToken, body: body})
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

func TestSessionsIntegration_MalformedCursor_Returns400(t *testing.T) {
	srv, _ := newSessionsServer(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "Asia/Colombo")

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/sessions?cursor=not-valid!!", bearer: adminToken})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", resp.status, resp.body)
	}
}

func TestSessionsIntegration_NoToken_Returns401(t *testing.T) {
	srv, _ := newSessionsServer(t)
	eventID, _ := createSessionsITEvent(t, srv, "Asia/Colombo")

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/sessions"})
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401: %s", resp.status, resp.body)
	}
}

// TestSessionsIntegration_DST_SpringForwardCrossing_CorrectLocalTimesAndDuration
// is FEATURES.md item 12's must-test, write-then-read, at the full stack:
// a session created with local wall-clock times spanning 2026-03-08's
// spring-forward (2:00am -> 3:00am, the 2-3am hour never happens) must
// read back with the correct per-instant offsets and the true 60-minute
// elapsed duration, not the naive 120-minute wall-clock difference.
func TestSessionsIntegration_DST_SpringForwardCrossing_CorrectLocalTimesAndDuration(t *testing.T) {
	srv, pool := newSessionsServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "America/New_York")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	sessionsPath := "/v1/events/" + eventID + "/sessions"
	createBody := sessionCreateBody(roomID, speakerID, "Spring forward", "2026-03-08T01:30:00", "2026-03-08T03:30:00")
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: sessionsPath, bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, sessionsPath, createBody, resp.status, resp.header, resp.body)

	var created struct {
		ID              string `json:"id"`
		StartsAt        string `json:"starts_at"`
		EndsAt          string `json:"ends_at"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created session: %v", err)
	}
	assertSpringForwardResult(t, created.StartsAt, created.EndsAt, created.DurationMinutes)

	// Re-read to prove the same correctness holds on GET, not merely on
	// the create response.
	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: sessionsPath + "/" + created.ID, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}
	var got struct {
		StartsAt        string `json:"starts_at"`
		EndsAt          string `json:"ends_at"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding get response: %v", err)
	}
	assertSpringForwardResult(t, got.StartsAt, got.EndsAt, got.DurationMinutes)
}

func assertSpringForwardResult(t *testing.T, startsAt, endsAt string, durationMinutes int) {
	t.Helper()

	if durationMinutes != 60 {
		t.Fatalf("got duration_minutes %d, want 60 (actual elapsed time -- the 2-3am hour never happened, so the naive 120-minute wall-clock diff would be wrong)", durationMinutes)
	}

	start, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		t.Fatalf("parsing starts_at %q: %v", startsAt, err)
	}
	end, err := time.Parse(time.RFC3339, endsAt)
	if err != nil {
		t.Fatalf("parsing ends_at %q: %v", endsAt, err)
	}
	if _, offset := start.Zone(); offset != -5*3600 {
		t.Errorf("got starts_at offset %ds, want -05:00 (EST, before the transition)", offset)
	}
	if _, offset := end.Zone(); offset != -4*3600 {
		t.Errorf("got ends_at offset %ds, want -04:00 (EDT, after the transition)", offset)
	}
}

// TestSessionsIntegration_DST_FallBackCrossing_CorrectLocalTimesAndDuration
// is the opposite-direction half of the same must-test: 2026-11-01's
// fall-back (2:00am -> 1:00am, the 1-2am hour happens twice) must read
// back with the true 180-minute elapsed duration, not the naive
// 120-minute wall-clock difference.
func TestSessionsIntegration_DST_FallBackCrossing_CorrectLocalTimesAndDuration(t *testing.T) {
	srv, pool := newSessionsServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createSessionsITEvent(t, srv, "America/New_York")
	roomID := createSessionsITRoom(t, srv, eventID, adminToken)
	speakerID := createSessionsITSpeaker(t, pool)

	sessionsPath := "/v1/events/" + eventID + "/sessions"
	createBody := sessionCreateBody(roomID, speakerID, "Fall back", "2026-11-01T00:30:00", "2026-11-01T02:30:00")
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: sessionsPath, bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, sessionsPath, createBody, resp.status, resp.header, resp.body)

	var created struct {
		ID              string `json:"id"`
		StartsAt        string `json:"starts_at"`
		EndsAt          string `json:"ends_at"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created session: %v", err)
	}
	assertFallBackResult(t, created.StartsAt, created.EndsAt, created.DurationMinutes)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: sessionsPath + "/" + created.ID, bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("get: got status %d, want 200: %s", resp.status, resp.body)
	}
	var got struct {
		StartsAt        string `json:"starts_at"`
		EndsAt          string `json:"ends_at"`
		DurationMinutes int    `json:"duration_minutes"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding get response: %v", err)
	}
	assertFallBackResult(t, got.StartsAt, got.EndsAt, got.DurationMinutes)
}

func assertFallBackResult(t *testing.T, startsAt, endsAt string, durationMinutes int) {
	t.Helper()

	if durationMinutes != 180 {
		t.Fatalf("got duration_minutes %d, want 180 (actual elapsed time -- the 1-2am hour happens twice, so the naive 120-minute wall-clock diff would be wrong)", durationMinutes)
	}

	start, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		t.Fatalf("parsing starts_at %q: %v", startsAt, err)
	}
	end, err := time.Parse(time.RFC3339, endsAt)
	if err != nil {
		t.Fatalf("parsing ends_at %q: %v", endsAt, err)
	}
	if _, offset := start.Zone(); offset != -4*3600 {
		t.Errorf("got starts_at offset %ds, want -04:00 (EDT, before the transition)", offset)
	}
	if _, offset := end.Zone(); offset != -5*3600 {
		t.Errorf("got ends_at offset %ds, want -05:00 (EST, after the transition)", offset)
	}
}
