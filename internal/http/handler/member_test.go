package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

type fakeMemberService struct {
	createOut member.Member
	createErr error

	getOut member.Member
	getErr error

	listOut member.Page
	listErr error

	assignOut member.Member
	assignErr error

	removeErr error
}

func (f *fakeMemberService) CreateMember(ctx context.Context, actorID, eventID string, in member.CreateInput) (member.Member, error) {
	return f.createOut, f.createErr
}
func (f *fakeMemberService) GetMember(ctx context.Context, eventID, memberID string) (member.Member, error) {
	return f.getOut, f.getErr
}
func (f *fakeMemberService) ListMembers(ctx context.Context, eventID string, limit int, after *member.Cursor) (member.Page, error) {
	return f.listOut, f.listErr
}
func (f *fakeMemberService) AssignRole(ctx context.Context, actorID, eventID, memberID string, version int, role string) (member.Member, error) {
	return f.assignOut, f.assignErr
}
func (f *fakeMemberService) RemoveMember(ctx context.Context, actorID, eventID, memberID string, version int) error {
	return f.removeErr
}

func TestCreateMember_Success(t *testing.T) {
	svc := &fakeMemberService{createOut: member.Member{ID: "mem-1", Role: "attendee"}}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/members", strings.NewReader(`{"email":"person@example.com","role":"attendee"}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateMember(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	if got["id"] != "mem-1" {
		t.Fatalf("got %v", got)
	}
}

func TestCreateMember_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeMemberService{}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/members", strings.NewReader(`{}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateMember(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateMember_UserNotFound_Returns404(t *testing.T) {
	svc := &fakeMemberService{createErr: domain.ErrNotFound}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/members", strings.NewReader(`{"email":"nobody@example.com","role":"attendee"}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateMember(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateMember_Forbidden_Returns403(t *testing.T) {
	svc := &fakeMemberService{createErr: domain.ErrForbidden}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/members", strings.NewReader(`{"email":"person@example.com","role":"admin"}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateMember(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateMember_Conflict_Returns409WithAlreadyMemberCode(t *testing.T) {
	svc := &fakeMemberService{createErr: domain.ErrConflict}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/members", strings.NewReader(`{"email":"person@example.com","role":"attendee"}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateMember(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "already_member" {
		t.Fatalf("got code %v, want already_member", errBody["code"])
	}
}

func TestListMembers_Success(t *testing.T) {
	svc := &fakeMemberService{listOut: member.Page{Members: []member.Member{{ID: "mem-1"}}}}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/members", nil)), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListMembers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestListMembers_NoRoleInContext_Returns500 is the wiring-bug case,
// matching TestCreateEvent_NoActorInContext_Returns500's precedent: Authz
// must run before this handler and attach a role, or the presenter has no
// basis for its email-visibility decision.
func TestListMembers_NoRoleInContext_Returns500(t *testing.T) {
	svc := &fakeMemberService{}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/members", nil))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListMembers(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500 when no caller role is in context", rec.Code)
	}
}

func TestListMembers_MalformedCursor_Returns400(t *testing.T) {
	svc := &fakeMemberService{}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/members?cursor=not-valid-base64!!", nil)), "admin")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListMembers(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestAssignRole_Success(t *testing.T) {
	svc := &fakeMemberService{assignOut: member.Member{ID: "mem-1", Role: "contributor"}}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestAssignRole_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeMemberService{}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestAssignRole_VersionMismatch_Returns409WithVersionConflictCode(t *testing.T) {
	svc := &fakeMemberService{assignErr: domain.ErrVersionMismatch}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "version_conflict" {
		t.Fatalf("got code %v, want version_conflict", errBody["code"])
	}
}

// TestAssignRole_VersionConflict_EmbedsCurrentMemberViaRoleAwarePresenter
// proves item 17's details.current goes through the SAME role-aware
// presenter (response.NewMemberResponse) the success path uses, driven by
// RoleFromContext -- never the raw domain row. The admin caller here is
// allowed to see email (D11), so this is the positive control: proves the
// re-fetched member actually lands under details.current at all, before
// the negative control below proves a non-admin caller's conflict body
// hides it.
func TestAssignRole_VersionConflict_EmbedsCurrentMemberViaRoleAwarePresenter(t *testing.T) {
	svc := &fakeMemberService{
		assignErr: domain.ErrVersionMismatch,
		getOut:    member.Member{ID: "mem-1", EventID: "evt-1", Role: "contributor", UserEmail: "target@example.com", UserName: "Target", Version: 3},
	}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	details := got["error"].(map[string]any)["details"].(map[string]any)
	current, ok := details["current"].(map[string]any)
	if !ok {
		t.Fatalf("details.current is not an object: %v", details["current"])
	}
	if current["user_email"] != "target@example.com" {
		t.Fatalf("got details.current.user_email %v, want target@example.com (admin caller should see it, D11)", current["user_email"])
	}
	if current["version"] != float64(3) {
		t.Fatalf("got details.current.version %v, want 3 (the fresh version, not the stale request's)", current["version"])
	}
}

// TestAssignRole_VersionConflict_NonAdminCallerRole_NoEmailKeyAnywhere is
// item 17's mandatory regression proof for the requirement it shares with
// item 10: details.current must be built by the SAME role-aware presenter
// as the success path, not a raw row -- so a version-conflict body can
// never smuggle out an email the success path would have redacted. In
// production, Authz's role_permissions rows (item 07) mean only 'admin'
// can ever reach AssignRole/RemoveMember at all -- a 'contributor' caller
// is rejected with 403 before this handler runs, so this scenario cannot
// occur end to end. This test bypasses that chokepoint deliberately (a
// fake service, no real Authz) to prove the presenter itself is
// defense-in-depth, matching this repo's existing precedent for testing a
// lower layer's guard even though a higher layer already blocks it in
// production (FAILURES.md's repo-level-guard note). The whole body is
// walked, not just details.current, in case a future change nests the
// email somewhere else in the envelope.
func TestAssignRole_VersionConflict_NonAdminCallerRole_NoEmailKeyAnywhere(t *testing.T) {
	svc := &fakeMemberService{
		assignErr: domain.ErrVersionMismatch,
		getOut:    member.Member{ID: "mem-1", EventID: "evt-1", Role: "contributor", UserEmail: "target@example.com", UserName: "Target", Version: 3},
	}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "contributor")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	assertNoKeyContaining(t, rec.Body.Bytes(), "email")
}

// TestAssignRole_VersionConflict_CurrentDeletedByWinner_DetailsCurrentIsNull
// covers item 17's (a): if the winning writer's change was itself a delete
// (RemoveMember), the re-fetch that builds details.current returns
// domain.ErrNotFound. That must not crash the handler or fall through to a
// generic 500 -- it's an expected outcome of concurrent writers, not a
// bug -- and details.current must be present and explicitly null, not
// silently omitted.
func TestAssignRole_VersionConflict_CurrentDeletedByWinner_DetailsCurrentIsNull(t *testing.T) {
	svc := &fakeMemberService{
		assignErr: domain.ErrVersionMismatch,
		getErr:    domain.ErrNotFound,
	}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	details := got["error"].(map[string]any)["details"].(map[string]any)
	current, exists := details["current"]
	if !exists {
		t.Fatal("details.current key is missing entirely, want present and explicitly null")
	}
	if current != nil {
		t.Fatalf("got details.current %v, want null (row was deleted by the winning writer)", current)
	}
}

func TestAssignRole_LastAdminConflict_Returns409WithLastAdminCode(t *testing.T) {
	svc := &fakeMemberService{assignErr: domain.ErrConflict}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "last_admin" {
		t.Fatalf("got code %v, want last_admin", errBody["code"])
	}
}

func TestAssignRole_NotFound_Returns404(t *testing.T) {
	svc := &fakeMemberService{assignErr: domain.ErrNotFound}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/members/mem-1", strings.NewReader(`{"role":"contributor","version":1}`))), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.AssignRole(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveMember_Success(t *testing.T) {
	svc := &fakeMemberService{}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/members/mem-1?version=1", nil)), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.RemoveMember(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204: %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveMember_MissingVersion_Returns422WithoutCallingService(t *testing.T) {
	svc := &fakeMemberService{}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/members/mem-1", nil)), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.RemoveMember(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveMember_LastAdminConflict_Returns409WithLastAdminCode(t *testing.T) {
	svc := &fakeMemberService{removeErr: domain.ErrConflict}
	h := handler.NewMemberHandler(svc, discardLogger())

	req := withRole(withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/members/mem-1?version=1", nil)), "admin")
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("memberId", "mem-1")
	rec := httptest.NewRecorder()

	h.RemoveMember(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "last_admin" {
		t.Fatalf("got code %v, want last_admin", errBody["code"])
	}
}

// assertNoKeyContaining walks the full JSON tree -- maps AND slices, at
// every nesting depth -- and fails if any object key contains substr,
// case-insensitively. Mirrors internal/http/response.assertNoKeyContaining
// and internal/http/events_integration_test.go's copy; duplicated rather
// than imported since each lives in a different package's _test.go file.
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
