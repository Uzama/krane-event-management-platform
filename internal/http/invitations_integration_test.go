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

// TestInvitationsIntegration_BulkInvite_MixedResultsAndRetryReplays is
// item 21's full-stack proof, against the real production router, the
// real mock OIDC issuer, and real Postgres: a batch of one new email and
// one already-invited email returns 207 with both outcomes, matches the
// OpenAPI contract, and a retry with the same Idempotency-Key and body
// replays the identical body without creating any new rows.
func TestInvitationsIntegration_BulkInvite_MixedResultsAndRetryReplays(t *testing.T) {
	srv, pool := newInvitationsServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createInvitationsITEvent(t, srv)

	alreadyInvited := eventsITUniqueSubject(t) + "-dup@example.com"
	seedBody := `{"email":"` + alreadyInvited + `","role":"attendee"}`
	if resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations", bearer: adminToken, body: seedBody}); resp.status != http.StatusCreated {
		t.Fatalf("seeding pre-existing invitation: got status %d, want 201: %s", resp.status, resp.body)
	}

	newEmail := eventsITUniqueSubject(t) + "-new@example.com"
	bulkBody := `{"invitations":[{"email":"` + newEmail + `","role":"attendee"},{"email":"` + alreadyInvited + `","role":"attendee"}]}`
	path := "/v1/events/" + eventID + "/invitations/bulk"
	idempotencyKey := "bulk-key-" + eventsITUniqueSubject(t)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: path, bearer: adminToken, body: bulkBody, idempotencyKey: idempotencyKey})
	if resp.status != http.StatusMultiStatus {
		t.Fatalf("first bulk invite: got status %d, want 207: %s", resp.status, resp.body)
	}
	assertSpecWithIdempotencyKey(t, spec, http.MethodPost, path, bulkBody, idempotencyKey, resp.status, resp.header, resp.body)

	var first struct {
		Results []struct {
			Email        string  `json:"email"`
			Status       string  `json:"status"`
			InvitationID *string `json:"invitation_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.body, &first); err != nil {
		t.Fatalf("decoding first response: %v", err)
	}
	if len(first.Results) != 2 || first.Results[0].Status != "created" || first.Results[1].Status != "conflict" {
		t.Fatalf("got %+v, want [created, conflict]", first.Results)
	}

	var countBefore int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM invitations WHERE event_id = $1`, eventID).Scan(&countBefore); err != nil {
		t.Fatalf("counting invitations: %v", err)
	}

	// The actual retry: same Idempotency-Key, same body as the first call.
	retryResp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: path, bearer: adminToken, body: bulkBody, idempotencyKey: idempotencyKey})
	if retryResp.status != http.StatusMultiStatus {
		t.Fatalf("retry: got status %d, want 207: %s", retryResp.status, retryResp.body)
	}
	if string(retryResp.body) != string(resp.body) {
		t.Fatalf("retry body differs from the original:\nfirst: %s\nretry: %s", resp.body, retryResp.body)
	}

	var countAfter int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM invitations WHERE event_id = $1`, eventID).Scan(&countAfter); err != nil {
		t.Fatalf("counting invitations: %v", err)
	}
	if countAfter != countBefore {
		t.Fatalf("invitation count changed after the retry: %d -> %d, want unchanged", countBefore, countAfter)
	}
}

func TestInvitationsIntegration_BulkInvite_MissingIdempotencyKey_Returns422(t *testing.T) {
	srv, _ := newInvitationsServer(t)
	eventID, adminToken := createInvitationsITEvent(t, srv)

	body := `{"invitations":[{"email":"` + eventsITUniqueSubject(t) + `@example.com","role":"attendee"}]}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations/bulk", bearer: adminToken, body: body})
	if resp.status != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", resp.status, resp.body)
	}
}

// TestInvitationsIntegration_BulkInvite_ContributorForbiddenOnOneItem
// proves item 21's requirement (a) at the full stack: the per-item
// escalation guard runs for each item independently, even for a caller
// who IS allowed through Authz (contributor has invitation:create) but
// is only allowed to invite at attendee.
func TestInvitationsIntegration_BulkInvite_ContributorForbiddenOnOneItem(t *testing.T) {
	srv, pool := newInvitationsServer(t)
	eventID, _ := createInvitationsITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	body := `{"invitations":[{"email":"` + eventsITUniqueSubject(t) + `-a@example.com","role":"attendee"},{"email":"` + eventsITUniqueSubject(t) + `-b@example.com","role":"admin"}]}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/invitations/bulk", bearer: contributorToken, body: body, idempotencyKey: "key-" + eventsITUniqueSubject(t)})
	if resp.status != http.StatusMultiStatus {
		t.Fatalf("got status %d, want 207: %s", resp.status, resp.body)
	}
	var got struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Results) != 2 || got.Results[0].Status != "created" || got.Results[1].Status != "forbidden" {
		t.Fatalf("got %+v, want [created, forbidden] -- a contributor must be forbidden from the admin-role item even inside a batch", got.Results)
	}
}
