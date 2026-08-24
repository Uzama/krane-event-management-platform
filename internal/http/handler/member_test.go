package handler_test

import (
	"context"
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

	listOut member.Page
	listErr error

	assignOut member.Member
	assignErr error

	removeErr error
}

func (f *fakeMemberService) CreateMember(ctx context.Context, actorID, eventID string, in member.CreateInput) (member.Member, error) {
	return f.createOut, f.createErr
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

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/members/mem-1?version=1", nil))
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

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/members/mem-1", nil))
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

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/members/mem-1?version=1", nil))
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
