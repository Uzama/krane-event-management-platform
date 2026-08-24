package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
	"github.com/Uzama/krane-event-management-platform/internal/http/middleware"
)

type fakeEventService struct {
	createOut event.Event
	createErr error

	getOut event.Event
	getErr error

	listOut   event.Page
	listErr   error
	gotUserID string
	gotLimit  int
	gotAfter  *event.Cursor

	updateOut event.Event
	updateErr error

	deleteErr    error
	deleteCalled bool
}

func (f *fakeEventService) CreateEvent(ctx context.Context, actorID string, in event.CreateInput) (event.Event, error) {
	return f.createOut, f.createErr
}
func (f *fakeEventService) GetEvent(ctx context.Context, id string) (event.Event, error) {
	return f.getOut, f.getErr
}
func (f *fakeEventService) ListEvents(ctx context.Context, userID string, limit int, after *event.Cursor) (event.Page, error) {
	f.gotUserID, f.gotLimit, f.gotAfter = userID, limit, after
	return f.listOut, f.listErr
}
func (f *fakeEventService) UpdateEvent(ctx context.Context, actorID, id string, version int, patch event.Patch) (event.Event, error) {
	return f.updateOut, f.updateErr
}
func (f *fakeEventService) DeleteEvent(ctx context.Context, actorID, id string, version int) (event.Event, error) {
	f.deleteCalled = true
	return event.Event{}, f.deleteErr
}

func withActor(req *http.Request) *http.Request {
	return req.WithContext(middleware.ContextWithUser(req.Context(), user.User{ID: "actor-1"}))
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
}

func TestCreateEvent_Success(t *testing.T) {
	svc := &fakeEventService{createOut: event.Event{ID: "evt-1", Name: "Conf"}}
	h := handler.NewEventHandler(svc, discardLogger())

	body := `{"name":"Conf","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(body)))
	rec := httptest.NewRecorder()

	h.CreateEvent(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	if got["id"] != "evt-1" {
		t.Fatalf("got %v", got)
	}
}

func TestCreateEvent_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeEventService{}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(`{}`)))
	rec := httptest.NewRecorder()

	h.CreateEvent(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateEvent_NoActorInContext_Returns500(t *testing.T) {
	svc := &fakeEventService{}
	h := handler.NewEventHandler(svc, discardLogger())

	body := `{"name":"Conf","timezone":"Asia/Colombo","starts_at":"2026-03-15T10:00:00Z","ends_at":"2026-03-15T11:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.CreateEvent(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500 (wiring bug: Auth must run first)", rec.Code)
	}
}

func TestGetEvent_Success(t *testing.T) {
	svc := &fakeEventService{getOut: event.Event{ID: "evt-1", Name: "Conf"}}
	h := handler.NewEventHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.GetEvent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestGetEvent_NotFound_Returns404(t *testing.T) {
	svc := &fakeEventService{getErr: domain.ErrNotFound}
	h := handler.NewEventHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.GetEvent(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestListEvents_Success_AppliesDefaultLimit(t *testing.T) {
	svc := &fakeEventService{listOut: event.Page{Events: []event.Event{{ID: "evt-1"}}}}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	rec := httptest.NewRecorder()

	h.ListEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.gotLimit != 25 {
		t.Fatalf("got limit %d, want default 25", svc.gotLimit)
	}
	if svc.gotUserID != "actor-1" {
		t.Fatalf("got userID %q, want actor-1", svc.gotUserID)
	}
}

func TestListEvents_ClampsLimitAboveMax(t *testing.T) {
	svc := &fakeEventService{}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodGet, "/v1/events?limit=1000", nil))
	rec := httptest.NewRecorder()

	h.ListEvents(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422 for a limit over the max", rec.Code)
	}
}

func TestListEvents_MalformedCursor_Returns400(t *testing.T) {
	svc := &fakeEventService{}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodGet, "/v1/events?cursor=not-a-real-cursor!!", nil))
	rec := httptest.NewRecorder()

	h.ListEvents(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400 for a malformed cursor: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchEvent_Success(t *testing.T) {
	svc := &fakeEventService{updateOut: event.Event{ID: "evt-1", Name: "New name", Version: 2}}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1", strings.NewReader(`{"version":1,"name":"New name"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.PatchEvent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchEvent_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeEventService{}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1", strings.NewReader(`{}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.PatchEvent(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchEvent_VersionMismatch_Returns409(t *testing.T) {
	svc := &fakeEventService{updateErr: domain.ErrVersionMismatch}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1", strings.NewReader(`{"version":1,"name":"New name"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.PatchEvent(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchEvent_NotFound_Returns404(t *testing.T) {
	svc := &fakeEventService{updateErr: domain.ErrNotFound}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1", strings.NewReader(`{"version":1,"name":"New name"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.PatchEvent(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteEvent_Success(t *testing.T) {
	svc := &fakeEventService{}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.DeleteEvent(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204: %s", rec.Code, rec.Body.String())
	}
}

// TestDeleteEvent_MissingOrInvalidVersion_Returns422WithoutCallingService is
// the lost-update-protection boundary for delete: every shape of "no usable
// version" must be rejected at the handler, before the service (and so the
// repository's version-gated UPDATE) is ever reached -- an unversioned
// delete must be structurally impossible, not just discouraged.
func TestDeleteEvent_MissingOrInvalidVersion_Returns422WithoutCallingService(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "version absent", path: "/v1/events/evt-1"},
		{name: "version empty", path: "/v1/events/evt-1?version="},
		{name: "version zero", path: "/v1/events/evt-1?version=0"},
		{name: "version negative", path: "/v1/events/evt-1?version=-1"},
		{name: "version non-numeric", path: "/v1/events/evt-1?version=abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeEventService{}
			h := handler.NewEventHandler(svc, discardLogger())

			req := withActor(httptest.NewRequest(http.MethodDelete, tc.path, nil))
			req.SetPathValue("eventId", "evt-1")
			rec := httptest.NewRecorder()

			h.DeleteEvent(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
			}
			if svc.deleteCalled {
				t.Fatal("service.DeleteEvent was called despite a missing/invalid version -- an unversioned delete must never reach the repository")
			}
		})
	}
}

func TestDeleteEvent_VersionMismatch_Returns409(t *testing.T) {
	svc := &fakeEventService{deleteErr: domain.ErrVersionMismatch}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.DeleteEvent(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteEvent_NotFound_Returns404(t *testing.T) {
	svc := &fakeEventService{deleteErr: domain.ErrNotFound}
	h := handler.NewEventHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.DeleteEvent(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
