package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

type fakeInvitationService struct {
	createOut invitation.Invitation
	createErr error

	listOut invitation.Page
	listErr error
}

func (f *fakeInvitationService) CreateInvitation(ctx context.Context, actorID, eventID string, in invitation.CreateInput) (invitation.Invitation, error) {
	return f.createOut, f.createErr
}
func (f *fakeInvitationService) ListInvitations(ctx context.Context, eventID string, limit int, after *invitation.Cursor) (invitation.Page, error) {
	return f.listOut, f.listErr
}

func TestCreateInvitation_Success(t *testing.T) {
	svc := &fakeInvitationService{createOut: invitation.Invitation{ID: "inv-1", Role: "attendee"}}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations", strings.NewReader(`{"email":"person@example.com","role":"attendee"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateInvitation(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	if got["id"] != "inv-1" {
		t.Fatalf("got %v", got)
	}
}

func TestCreateInvitation_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations", strings.NewReader(`{}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateInvitation(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateInvitation_Forbidden_Returns403WithCannotInviteAtRoleCode
// proves the escalation guard's error maps to 403, not 422 -- it's an
// authorization decision, not a validation one (per plan review).
func TestCreateInvitation_Forbidden_Returns403WithCannotInviteAtRoleCode(t *testing.T) {
	svc := &fakeInvitationService{createErr: domain.ErrForbidden}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations", strings.NewReader(`{"email":"person@example.com","role":"admin"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateInvitation(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "cannot_invite_at_role" {
		t.Fatalf("got code %v, want cannot_invite_at_role", errBody["code"])
	}
}

func TestCreateInvitation_Conflict_Returns409WithAlreadyInvitedCode(t *testing.T) {
	svc := &fakeInvitationService{createErr: domain.ErrConflict}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations", strings.NewReader(`{"email":"person@example.com","role":"attendee"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateInvitation(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "already_invited" {
		t.Fatalf("got code %v, want already_invited", errBody["code"])
	}
}

func TestCreateInvitation_NoActorInContext_Returns500(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations", strings.NewReader(`{"email":"person@example.com","role":"attendee"}`))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateInvitation(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500 when no actor is in context", rec.Code)
	}
}

func TestListInvitations_Success(t *testing.T) {
	svc := &fakeInvitationService{listOut: invitation.Page{Invitations: []invitation.Invitation{{ID: "inv-1"}}}}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/invitations", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListInvitations(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestListInvitations_MalformedCursor_Returns400(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/invitations?cursor=not-valid-base64!!", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListInvitations(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
