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
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

// This exercises the real production router with the real mock OIDC issuer
// and real Postgres, same shape as rooms_integration_test.go -- item 13 is
// the first real consumer of the /v1/events/{eventId}/invitations routes.
// Reuses events_integration_test.go's shared helpers since this file is in
// the same http_test package.

// newInvitationsServer wires the Event and Invitation handlers with real
// dependencies -- invitation tests need to create fixture events through
// the real stack, then exercise invitation routes on them.
func newInvitationsServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
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
	invitations := invitation.NewService(postgres.NewInvitationRepository(pool))
	logger := discardLogger()

	router := apihttp.NewRouter(apihttp.RouterDeps{
		Event:        handler.NewEventHandler(events, logger),
		Invitation:   handler.NewInvitationHandler(invitations, logger),
		AuthVerifier: verifier,
		Users:        users,
		Authz:        policy,
		Logger:       logger,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, pool
}

// createInvitationsITEvent creates a fixture event through the real stack,
// returning its id and the creator's (auto-admin) bearer token.
func createInvitationsITEvent(t *testing.T, srv *httptest.Server) (eventID, adminToken string) {
	t.Helper()

	adminSub := eventsITUniqueSubject(t)
	adminToken = mintEventsITToken(t, adminSub)

	createBody := `{"name":"Invitations IT","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
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

// TestInvitationsIntegration_AdminInvitesAtAnyRole proves the matrix's
// admin row (invitation:create/read) at the real routes, including
// inviting at an elevated role.
func TestInvitationsIntegration_AdminInvitesAtAnyRole(t *testing.T) {
	srv, _ := newInvitationsServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createInvitationsITEvent(t, srv)

	createBody := `{"email":"invitee-` + eventsITUniqueSubject(t) + `@example.com","role":"admin"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: adminToken, body: createBody})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, "/v1/events/"+eventID+"/invitations", createBody, resp.status, resp.header, resp.body)

	var created struct {
		ID     string `json:"id"`
		Role   string `json:"role"`
		UserID any    `json:"user_id"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding created invitation: %v", err)
	}
	if created.Role != "admin" {
		t.Fatalf("got role %q, want admin", created.Role)
	}
	if created.UserID != nil {
		t.Fatalf("got user_id %v, want null (the invitee has never signed in)", created.UserID)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/invitations", bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("list: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, "/v1/events/"+eventID+"/invitations", "", resp.status, resp.header, resp.body)
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &page); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	found := false
	for _, inv := range page.Data {
		if inv["id"] == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created invitation %q missing from list: %s", created.ID, resp.body)
	}
}

// TestInvitationsIntegration_ContributorCanInviteAttendeeOnly proves the
// escalation guard at the real routes, not just at the repository: a
// contributor may invite attendee (201) but is rejected as
// cannot_invite_at_role (403, not 422) inviting admin or contributor.
func TestInvitationsIntegration_ContributorCanInviteAttendeeOnly(t *testing.T) {
	srv, pool := newInvitationsServer(t)
	eventID, _ := createInvitationsITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	resp := doEventsITRequest(t, srv, eventsITRequest{
		method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: contributorToken,
		body: `{"email":"attendee-` + eventsITUniqueSubject(t) + `@example.com","role":"attendee"}`,
	})
	if resp.status != http.StatusCreated {
		t.Fatalf("contributor inviting attendee: got status %d, want 201: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{
		method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: contributorToken,
		body: `{"email":"escalation-` + eventsITUniqueSubject(t) + `@example.com","role":"admin"}`,
	})
	if resp.status != http.StatusForbidden {
		t.Fatalf("contributor inviting admin: got status %d, want 403: %s", resp.status, resp.body)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if got.Error.Code != "cannot_invite_at_role" {
		t.Fatalf("got code %q, want cannot_invite_at_role", got.Error.Code)
	}
}

// TestInvitationsIntegration_Attendee_ForbiddenOnBothRoutes proves the
// matrix's attendee row (no invitation permission rows at all) at both
// routes, through real requests.
func TestInvitationsIntegration_Attendee_ForbiddenOnBothRoutes(t *testing.T) {
	srv, pool := newInvitationsServer(t)
	eventID, _ := createInvitationsITEvent(t, srv)

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	resp := doEventsITRequest(t, srv, eventsITRequest{
		method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: attendeeToken,
		body: `{"email":"nobody-` + eventsITUniqueSubject(t) + `@example.com","role":"attendee"}`,
	})
	if resp.status != http.StatusForbidden {
		t.Fatalf("create: got status %d, want 403: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/invitations", bearer: attendeeToken})
	if resp.status != http.StatusForbidden {
		t.Fatalf("list: got status %d, want 403: %s", resp.status, resp.body)
	}
}

// TestInvitationsIntegration_DuplicateEmail_Returns409AlreadyInvited proves
// the row-level unique constraint (event_id, email) maps to 409 at the
// real route.
func TestInvitationsIntegration_DuplicateEmail_Returns409AlreadyInvited(t *testing.T) {
	srv, _ := newInvitationsServer(t)
	eventID, adminToken := createInvitationsITEvent(t, srv)

	body := `{"email":"dup-` + eventsITUniqueSubject(t) + `@example.com","role":"attendee"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: adminToken, body: body})
	if resp.status != http.StatusCreated {
		t.Fatalf("first invite: got status %d, want 201: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: adminToken, body: body})
	if resp.status != http.StatusConflict {
		t.Fatalf("duplicate invite: got status %d, want 409: %s", resp.status, resp.body)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if got.Error.Code != "already_invited" {
		t.Fatalf("got code %q, want already_invited", got.Error.Code)
	}
}

func TestInvitationsIntegration_MalformedCursor_Returns400(t *testing.T) {
	srv, _ := newInvitationsServer(t)
	eventID, adminToken := createInvitationsITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/invitations?cursor=not-valid!!", bearer: adminToken})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", resp.status, resp.body)
	}
}

func TestInvitationsIntegration_NoToken_Returns401(t *testing.T) {
	srv, _ := newInvitationsServer(t)
	eventID, _ := createInvitationsITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/invitations"})
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401: %s", resp.status, resp.body)
	}
}
