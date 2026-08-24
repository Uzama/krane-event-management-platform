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

	bulkOut    invitation.BulkResult
	bulkErr    error
	bulkActor  string
	bulkEvent  string
	bulkKey    string
	bulkHash   string
	bulkItems  []invitation.CreateInput
	bulkCalled bool
}

func (f *fakeInvitationService) CreateInvitation(ctx context.Context, actorID, eventID string, in invitation.CreateInput) (invitation.Invitation, error) {
	return f.createOut, f.createErr
}
func (f *fakeInvitationService) ListInvitations(ctx context.Context, eventID string, limit int, after *invitation.Cursor) (invitation.Page, error) {
	return f.listOut, f.listErr
}
func (f *fakeInvitationService) BulkInvite(ctx context.Context, actorID, eventID, idempotencyKey, requestHash string, items []invitation.CreateInput) (invitation.BulkResult, error) {
	f.bulkCalled = true
	f.bulkActor, f.bulkEvent, f.bulkKey, f.bulkHash, f.bulkItems = actorID, eventID, idempotencyKey, requestHash, items
	return f.bulkOut, f.bulkErr
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

func TestBulkCreateInvitations_Success(t *testing.T) {
	id := "inv-1"
	svc := &fakeInvitationService{bulkOut: invitation.BulkResult{Items: []invitation.BulkItemResult{
		{Email: "a@example.com", Status: "created", InvitationID: &id},
	}}}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(`{"invitations":[{"email":"a@example.com","role":"attendee"}]}`)))
	req.Header.Set("Idempotency-Key", "key-1")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.BulkCreateInvitations(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("got status %d, want 207: %s", rec.Code, rec.Body.String())
	}
	if !svc.bulkCalled {
		t.Fatal("service.BulkInvite was not called")
	}
	if svc.bulkKey != "key-1" || svc.bulkEvent != "evt-1" {
		t.Fatalf("got key=%q event=%q, want key-1/evt-1", svc.bulkKey, svc.bulkEvent)
	}
	if svc.bulkHash == "" {
		t.Fatal("requestHash was not computed")
	}
	if len(svc.bulkItems) != 1 || svc.bulkItems[0].Email != "a@example.com" {
		t.Fatalf("got items %+v", svc.bulkItems)
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	results := got["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %v", len(results), got)
	}
}

// TestBulkCreateInvitations_SameBodyProducesSameHash proves requestHash is
// computed deterministically from the raw body -- two identical requests
// must hash identically, since that's what makes a retry with the same
// Idempotency-Key detectable as "the same request" at all.
func TestBulkCreateInvitations_SameBodyProducesSameHash(t *testing.T) {
	body := `{"invitations":[{"email":"a@example.com","role":"attendee"}]}`

	var hashes [2]string
	for i := range hashes {
		svc := &fakeInvitationService{}
		h := handler.NewInvitationHandler(svc, discardLogger())
		req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(body)))
		req.Header.Set("Idempotency-Key", "key-1")
		req.SetPathValue("eventId", "evt-1")
		h.BulkCreateInvitations(httptest.NewRecorder(), req)
		hashes[i] = svc.bulkHash
	}
	if hashes[0] == "" || hashes[0] != hashes[1] {
		t.Fatalf("got hashes %v, want two identical non-empty hashes", hashes)
	}
}

func TestBulkCreateInvitations_MissingIdempotencyKey_Returns422WithoutCallingService(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(`{"invitations":[{"email":"a@example.com","role":"attendee"}]}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.BulkCreateInvitations(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if svc.bulkCalled {
		t.Fatal("service.BulkInvite was called despite a missing Idempotency-Key")
	}
}

func TestBulkCreateInvitations_EmptyInvitationsList_Returns422WithoutCallingService(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(`{"invitations":[]}`)))
	req.Header.Set("Idempotency-Key", "key-1")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.BulkCreateInvitations(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if svc.bulkCalled {
		t.Fatal("service.BulkInvite was called despite an empty invitations list")
	}
}

func TestBulkCreateInvitations_InvalidItem_Returns422WithoutCallingService(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(`{"invitations":[{"email":"","role":"attendee"}]}`)))
	req.Header.Set("Idempotency-Key", "key-1")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.BulkCreateInvitations(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if svc.bulkCalled {
		t.Fatal("service.BulkInvite was called despite an invalid item")
	}
}

func TestBulkCreateInvitations_IdempotencyKeyConflict_Returns422(t *testing.T) {
	svc := &fakeInvitationService{bulkErr: domain.ErrConflict}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(`{"invitations":[{"email":"a@example.com","role":"attendee"}]}`)))
	req.Header.Set("Idempotency-Key", "key-1")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.BulkCreateInvitations(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	errBody := got["error"].(map[string]any)
	if errBody["code"] != "idempotency_key_conflict" {
		t.Fatalf("got code %v, want idempotency_key_conflict", errBody["code"])
	}
}

func TestBulkCreateInvitations_NoActorInContext_Returns500(t *testing.T) {
	svc := &fakeInvitationService{}
	h := handler.NewInvitationHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/invitations/bulk", strings.NewReader(`{"invitations":[{"email":"a@example.com","role":"attendee"}]}`))
	req.Header.Set("Idempotency-Key", "key-1")
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.BulkCreateInvitations(rec, req)

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
