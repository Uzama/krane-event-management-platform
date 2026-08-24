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
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	apihttp "github.com/Uzama/krane-event-management-platform/internal/http"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

// This exercises the real production router with the real mock OIDC issuer
// and real Postgres, same shape as events_integration_test.go -- item 09 is
// the first real consumer of the /v1/events/{eventId}/members routes.
// Reuses events_integration_test.go's shared helpers (mintEventsITToken,
// doEventsITRequest, assertSpec, eventsITUniqueSubject, etc.) since this
// file is in the same http_test package.

// newMembersServer wires both Event and Member handlers with real
// dependencies -- member tests need to create a fixture event through the
// real stack, then exercise membership routes on it.
func newMembersServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
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
	members := member.NewService(postgres.NewMemberRepository(pool))
	logger := discardLogger()

	router := apihttp.NewRouter(apihttp.RouterDeps{
		Event:        handler.NewEventHandler(events, logger),
		Member:       handler.NewMemberHandler(members, logger),
		AuthVerifier: verifier,
		Users:        users,
		Authz:        policy,
		Logger:       logger,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, pool
}

// createMembersITEvent creates a fixture event through the real stack,
// returning its id and the creator's (auto-admin) bearer token.
func createMembersITEvent(t *testing.T, srv *httptest.Server) (eventID, adminToken string) {
	t.Helper()

	adminSub := eventsITUniqueSubject(t)
	adminToken = mintEventsITToken(t, adminSub)

	createBody := `{"name":"Members IT","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
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

func TestMembersIntegration_AdminAddsMember_Returns201(t *testing.T) {
	srv, _ := newMembersServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	targetSub := eventsITUniqueSubject(t)
	targetToken := mintEventsITToken(t, targetSub)
	// Minting a token creates the user via the mock issuer's real sign-in
	// path is not exercised here -- Auth's user-resolution middleware
	// creates the users row on first authenticated request, so establish
	// the target user by making one authenticated call first.
	doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events", bearer: targetToken})

	targetEmail := targetSub + "@test.krane"
	body := fmt.Sprintf(`{"email":%q,"role":"attendee"}`, targetEmail)
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/members", bearer: adminToken, body: body})
	if resp.status != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodPost, "/v1/events/"+eventID+"/members", body, resp.status, resp.header, resp.body)

	var created struct {
		Role      string `json:"role"`
		UserEmail string `json:"user_email"`
	}
	if err := json.Unmarshal(resp.body, &created); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if created.Role != "attendee" || created.UserEmail != targetEmail {
		t.Fatalf("got %+v, want role=attendee user_email=%s", created, targetEmail)
	}
}

func TestMembersIntegration_Contributor_ForbiddenFromCreate(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, _ := createMembersITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	targetSub := eventsITUniqueSubject(t)
	body := fmt.Sprintf(`{"email":%q,"role":"attendee"}`, targetSub+"@test.krane")
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/members", bearer: contributorToken, body: body})
	if resp.status != http.StatusForbidden {
		t.Fatalf("got status %d, want 403: %s", resp.status, resp.body)
	}
}

func TestMembersIntegration_NonMember_ForbiddenFromCreate(t *testing.T) {
	srv, _ := newMembersServer(t)
	eventID, _ := createMembersITEvent(t, srv)

	outsiderSub := eventsITUniqueSubject(t)
	outsiderToken := mintEventsITToken(t, outsiderSub)

	body := `{"email":"nobody@test.krane","role":"attendee"}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPost, path: "/v1/events/" + eventID + "/members", bearer: outsiderToken, body: body})
	if resp.status != http.StatusForbidden {
		t.Fatalf("got status %d, want 403: %s", resp.status, resp.body)
	}
}

// TestMembersIntegration_Contributor_ForbiddenFromAssignRole reproves item
// 07's own must: at the real route (previously only proved against a
// throwaway handler) -- a contributor gets 403 attempting a role change.
func TestMembersIntegration_Contributor_ForbiddenFromAssignRole(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, _ := createMembersITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	contributorMemberID := seedEventsITMemberReturningID(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	body := `{"role":"admin","version":1}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + eventID + "/members/" + contributorMemberID, bearer: contributorToken, body: body})
	if resp.status != http.StatusForbidden {
		t.Fatalf("got status %d, want 403: %s", resp.status, resp.body)
	}
}

func TestMembersIntegration_Roster_AdminAndContributorCanRead_AttendeeCannot(t *testing.T) {
	srv, pool := newMembersServer(t)
	spec := loadEventsITSpec(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, attendeeSub, "attendee")
	attendeeToken := mintEventsITToken(t, attendeeSub)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members", bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("admin list: got status %d, want 200: %s", resp.status, resp.body)
	}
	assertSpec(t, spec, http.MethodGet, "/v1/events/"+eventID+"/members", "", resp.status, resp.header, resp.body)

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members", bearer: contributorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("contributor list: got status %d, want 200: %s", resp.status, resp.body)
	}

	resp = doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members", bearer: attendeeToken})
	if resp.status != http.StatusForbidden {
		t.Fatalf("attendee list: got status %d, want 403: %s", resp.status, resp.body)
	}
}

// TestMembersIntegration_ContributorRoster_NoEmailKeyAnywhere is item 10's
// must: test (a) at the real, full stack: real OIDC token, real Postgres,
// real Authz -> presenter chain. D11 (docs/requirements.md §7.2): email is
// admin-only PII, so a contributor listing the roster must get back a body
// with no key containing "email" anywhere, at any nesting depth -- not
// merely blank user_email fields.
func TestMembersIntegration_ContributorRoster_NoEmailKeyAnywhere(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, _ := createMembersITEvent(t, srv)

	contributorSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, contributorSub, "contributor")
	contributorToken := mintEventsITToken(t, contributorSub)

	// A populated roster, not just the fixture's lone admin -- an attendee
	// too, so the assertion is proven against real email-bearing rows, not
	// vacuously on a near-empty roster.
	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, attendeeSub, "attendee")

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members", bearer: contributorToken})
	if resp.status != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", resp.status, resp.body)
	}
	assertNoKeyContaining(t, resp.body, "email")
}

// TestMembersIntegration_AdminRoster_IncludesEmail guards against
// over-redaction -- D11 grants admin visibility, and this must keep
// passing alongside the contributor test above, or the presenter has
// collapsed to "nobody sees email."
func TestMembersIntegration_AdminRoster_IncludesEmail(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	attendeeSub := eventsITUniqueSubject(t)
	seedEventsITMember(t, pool, eventID, attendeeSub, "attendee")

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members", bearer: adminToken})
	if resp.status != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", resp.status, resp.body)
	}

	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &page); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(page.Data) == 0 {
		t.Fatal("roster is empty, want at least the admin and the seeded attendee")
	}
	for _, m := range page.Data {
		if _, ok := m["user_email"]; !ok {
			t.Errorf("member %v missing user_email; admin must see it", m)
		}
	}
}

func TestMembersIntegration_DemotingSoleAdmin_Returns409LastAdmin(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	var adminMemberID string
	err := pool.QueryRow(context.Background(), `SELECT id::text FROM event_members WHERE event_id = $1 AND role = 'admin' LIMIT 1`, eventID).Scan(&adminMemberID)
	if err != nil {
		t.Fatalf("looking up admin membership: %v", err)
	}

	body := `{"role":"contributor","version":1}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + eventID + "/members/" + adminMemberID, bearer: adminToken, body: body})
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
	if got.Error.Code != "last_admin" {
		t.Fatalf("got code %q, want last_admin", got.Error.Code)
	}
}

func TestMembersIntegration_RemovingSoleAdmin_Returns409LastAdmin(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	var adminMemberID string
	err := pool.QueryRow(context.Background(), `SELECT id::text FROM event_members WHERE event_id = $1 AND role = 'admin' LIMIT 1`, eventID).Scan(&adminMemberID)
	if err != nil {
		t.Fatalf("looking up admin membership: %v", err)
	}

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: "/v1/events/" + eventID + "/members/" + adminMemberID + "?version=1", bearer: adminToken})
	if resp.status != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", resp.status, resp.body)
	}
}

func TestMembersIntegration_StaleVersionPatch_Returns409VersionConflict(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	targetSub := eventsITUniqueSubject(t)
	targetMemberID := seedEventsITMemberReturningID(t, pool, eventID, targetSub, "attendee")

	body := `{"role":"contributor","version":999}`
	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodPatch, path: "/v1/events/" + eventID + "/members/" + targetMemberID, bearer: adminToken, body: body})
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

func TestMembersIntegration_StaleVersionDelete_Returns409VersionConflict(t *testing.T) {
	srv, pool := newMembersServer(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	targetSub := eventsITUniqueSubject(t)
	targetMemberID := seedEventsITMemberReturningID(t, pool, eventID, targetSub, "attendee")

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodDelete, path: "/v1/events/" + eventID + "/members/" + targetMemberID + "?version=999", bearer: adminToken})
	if resp.status != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", resp.status, resp.body)
	}
}

func TestMembersIntegration_MalformedCursor_Returns400(t *testing.T) {
	srv, _ := newMembersServer(t)
	eventID, adminToken := createMembersITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members?cursor=not-valid!!", bearer: adminToken})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", resp.status, resp.body)
	}
}

func TestMembersIntegration_NoToken_Returns401(t *testing.T) {
	srv, _ := newMembersServer(t)
	eventID, _ := createMembersITEvent(t, srv)

	resp := doEventsITRequest(t, srv, eventsITRequest{method: http.MethodGet, path: "/v1/events/" + eventID + "/members"})
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401: %s", resp.status, resp.body)
	}
}

// seedEventsITMemberReturningID mirrors seedEventsITMember (events_integration_test.go)
// but returns the created event_members row's own id, which member routes
// address directly ({memberId}).
func seedEventsITMemberReturningID(t *testing.T, pool *pgxpool.Pool, eventID, subject, role string) string {
	t.Helper()

	users := user.NewService(postgres.NewUserRepository(pool))
	u, err := users.GetOrCreateBySubject(context.Background(), subject, subject+"@test.krane", "Members IT User")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	var memberID string
	err = pool.QueryRow(context.Background(),
		`INSERT INTO event_members (event_id, user_id, role) VALUES ($1, $2, $3) RETURNING id::text`,
		eventID, u.ID, role,
	).Scan(&memberID)
	if err != nil {
		t.Fatalf("seeding event_members: %v", err)
	}
	return memberID
}
