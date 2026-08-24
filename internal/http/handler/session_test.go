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
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

func newYorkEvent(id string) event.Event {
	return event.Event{ID: id, Name: "Conf", Timezone: "America/New_York"}
}

type fakeEventGetter struct {
	getOut event.Event
	getErr error
}

func (f *fakeEventGetter) GetEvent(ctx context.Context, id string) (event.Event, error) {
	return f.getOut, f.getErr
}

type fakeSessionService struct {
	createOut session.Session
	createErr error

	getOut session.Session
	getErr error

	listOut    session.Page
	listErr    error
	gotEventID string
	gotLimit   int
	gotAfter   *session.Cursor

	updateOut session.Session
	updateErr error

	deleteOut    session.Session
	deleteErr    error
	deleteCalled bool

	seriesOut   session.Series
	seriesItems []session.SeriesOccurrenceResult
	seriesErr   error
}

func (f *fakeSessionService) CreateSession(ctx context.Context, actorID, eventID string, in session.CreateInput) (session.Session, error) {
	return f.createOut, f.createErr
}
func (f *fakeSessionService) GetSession(ctx context.Context, eventID, sessionID string) (session.Session, error) {
	return f.getOut, f.getErr
}
func (f *fakeSessionService) ListSessions(ctx context.Context, eventID string, limit int, after *session.Cursor) (session.Page, error) {
	f.gotEventID, f.gotLimit, f.gotAfter = eventID, limit, after
	return f.listOut, f.listErr
}
func (f *fakeSessionService) UpdateSession(ctx context.Context, actorID, eventID, sessionID string, version int, patch session.Patch) (session.Session, error) {
	return f.updateOut, f.updateErr
}
func (f *fakeSessionService) DeleteSession(ctx context.Context, actorID, eventID, sessionID string, version int) (session.Session, error) {
	f.deleteCalled = true
	return f.deleteOut, f.deleteErr
}
func (f *fakeSessionService) CreateSeries(ctx context.Context, actorID, eventID string, in session.SeriesCreateInput) (session.Series, []session.SeriesOccurrenceResult, error) {
	return f.seriesOut, f.seriesItems, f.seriesErr
}

func validCreateSessionBody() string {
	return `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","starts_at":"2026-06-15T09:00:00","ends_at":"2026-06-15T10:00:00"}`
}

func TestCreateSession_Success(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{createOut: session.Session{ID: "session-1", Title: "Keynote"}}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(validCreateSessionBody())))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if got["id"] != "session-1" {
		t.Fatalf("got %v", got)
	}
}

func validCreateSeriesBody() string {
	return `{"room_id":"room-1","speaker_id":"speaker-1","title":"Standup","first_starts_at":"2026-06-15T09:00:00","first_ends_at":"2026-06-15T09:30:00","freq":"daily","interval_count":1,"occurrences":5}`
}

func TestCreateSeries_Success(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessionID := "session-1"
	sessions := &fakeSessionService{
		seriesOut:   session.Series{ID: "series-1", Occurrences: 5},
		seriesItems: []session.SeriesOccurrenceResult{{Status: "created", SessionID: &sessionID}},
	}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions/series", strings.NewReader(validCreateSeriesBody())))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSeries(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if got["id"] != "series-1" {
		t.Fatalf("got %v", got)
	}
	occurrences, ok := got["occurrences"].([]any)
	if !ok || len(occurrences) != 1 {
		t.Fatalf("got occurrences %v, want 1 item", got["occurrences"])
	}
}

func TestCreateSeries_InvalidFreq_Returns422(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Standup","first_starts_at":"2026-06-15T09:00:00","first_ends_at":"2026-06-15T09:30:00","freq":"monthly","interval_count":1,"occurrences":5}`
	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions/series", strings.NewReader(body)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSeries(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSeries_OccurrencesOutOfRange_Returns422(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Standup","first_starts_at":"2026-06-15T09:00:00","first_ends_at":"2026-06-15T09:30:00","freq":"daily","interval_count":1,"occurrences":53}`
	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions/series", strings.NewReader(body)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSeries(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSession_InvalidBody_Returns422(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(`{}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSession_NonexistentLocalTime_Returns422(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	body := `{"room_id":"room-1","speaker_id":"speaker-1","title":"Keynote","starts_at":"2026-03-08T02:30:00","ends_at":"2026-03-08T04:00:00"}`
	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(body)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422 for a spring-forward gap time: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSession_NoActorInContext_Returns500(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(validCreateSessionBody()))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500 (wiring bug: Auth must run first)", rec.Code)
	}
}

func TestCreateSession_EventNotFound_Returns404(t *testing.T) {
	events := &fakeEventGetter{getErr: domain.ErrNotFound}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(validCreateSessionBody())))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSession_InvalidRoom_Returns404WithRoomNotFoundCode(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{createErr: session.ErrInvalidRoom}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(validCreateSessionBody())))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	errObj, _ := got["error"].(map[string]any)
	if errObj["code"] != "room_not_found" {
		t.Fatalf("got error.code %v, want room_not_found", errObj["code"])
	}
}

func TestCreateSession_InvalidSpeaker_Returns404WithSpeakerNotFoundCode(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{createErr: session.ErrInvalidSpeaker}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/sessions", strings.NewReader(validCreateSessionBody())))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	errObj, _ := got["error"].(map[string]any)
	if errObj["code"] != "speaker_not_found" {
		t.Fatalf("got error.code %v, want speaker_not_found", errObj["code"])
	}
}

func TestGetSession_Success(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{getOut: session.Session{ID: "session-1", Title: "Keynote"}}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/sessions/session-1", nil)
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.GetSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestGetSession_NotFound_Returns404(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{getErr: domain.ErrNotFound}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/sessions/session-1", nil)
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.GetSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestListSessions_Success_AppliesDefaultLimit(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{listOut: session.Page{Sessions: []session.Session{{ID: "session-1"}}}}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/sessions", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if sessions.gotLimit != 25 {
		t.Fatalf("got limit %d, want default 25", sessions.gotLimit)
	}
	if sessions.gotEventID != "evt-1" {
		t.Fatalf("got eventID %q, want evt-1", sessions.gotEventID)
	}
}

func TestListSessions_MalformedCursor_Returns400(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/sessions?cursor=not-a-real-cursor!!", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListSessions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400 for a malformed cursor: %s", rec.Code, rec.Body.String())
	}
}

func TestListSessions_EventNotFound_Returns404(t *testing.T) {
	events := &fakeEventGetter{getErr: domain.ErrNotFound}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/sessions", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListSessions(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchSession_Success(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{updateOut: session.Session{ID: "session-1", Title: "New title", Version: 2}}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/sessions/session-1", strings.NewReader(`{"version":1,"title":"New title"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.PatchSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchSession_InvalidBody_Returns422(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/sessions/session-1", strings.NewReader(`{}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.PatchSession(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchSession_VersionMismatch_Returns409(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{updateErr: domain.ErrVersionMismatch}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/sessions/session-1", strings.NewReader(`{"version":1,"title":"New title"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.PatchSession(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchSession_NotFound_Returns404(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{updateErr: domain.ErrNotFound}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/sessions/session-1", strings.NewReader(`{"version":1,"title":"New title"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.PatchSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSession_Success(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/sessions/session-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.DeleteSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if !sessions.deleteCalled {
		t.Fatal("service.DeleteSession was not called")
	}
}

func TestDeleteSession_MissingOrInvalidVersion_Returns422WithoutCallingService(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "version absent", path: "/v1/events/evt-1/sessions/session-1"},
		{name: "version empty", path: "/v1/events/evt-1/sessions/session-1?version="},
		{name: "version zero", path: "/v1/events/evt-1/sessions/session-1?version=0"},
		{name: "version negative", path: "/v1/events/evt-1/sessions/session-1?version=-1"},
		{name: "version non-numeric", path: "/v1/events/evt-1/sessions/session-1?version=abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
			sessions := &fakeSessionService{}
			h := handler.NewSessionHandler(sessions, events, discardLogger())

			req := withActor(httptest.NewRequest(http.MethodDelete, tc.path, nil))
			req.SetPathValue("eventId", "evt-1")
			req.SetPathValue("sessionId", "session-1")
			rec := httptest.NewRecorder()

			h.DeleteSession(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
			}
			if sessions.deleteCalled {
				t.Fatal("service.DeleteSession was called despite invalid version")
			}
		})
	}
}

func TestDeleteSession_NotFound_Returns404(t *testing.T) {
	events := &fakeEventGetter{getOut: newYorkEvent("evt-1")}
	sessions := &fakeSessionService{deleteErr: domain.ErrNotFound}
	h := handler.NewSessionHandler(sessions, events, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/sessions/session-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("sessionId", "session-1")
	rec := httptest.NewRecorder()

	h.DeleteSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
