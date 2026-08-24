package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/http/handler"
)

type fakeRoomService struct {
	createOut room.Room
	createErr error

	getOut room.Room
	getErr error

	listOut    room.Page
	listErr    error
	gotEventID string
	gotLimit   int
	gotAfter   *room.Cursor

	updateOut room.Room
	updateErr error

	deleteErr    error
	deleteCalled bool
}

func (f *fakeRoomService) CreateRoom(ctx context.Context, actorID, eventID string, in room.CreateInput) (room.Room, error) {
	return f.createOut, f.createErr
}
func (f *fakeRoomService) GetRoom(ctx context.Context, eventID, roomID string) (room.Room, error) {
	return f.getOut, f.getErr
}
func (f *fakeRoomService) ListRooms(ctx context.Context, eventID string, limit int, after *room.Cursor) (room.Page, error) {
	f.gotEventID, f.gotLimit, f.gotAfter = eventID, limit, after
	return f.listOut, f.listErr
}
func (f *fakeRoomService) UpdateRoom(ctx context.Context, actorID, eventID, roomID string, version int, patch room.Patch) (room.Room, error) {
	return f.updateOut, f.updateErr
}
func (f *fakeRoomService) DeleteRoom(ctx context.Context, actorID, eventID, roomID string, version int) error {
	f.deleteCalled = true
	return f.deleteErr
}

func TestCreateRoom_Success(t *testing.T) {
	svc := &fakeRoomService{createOut: room.Room{ID: "room-1", Name: "Hall A"}}
	h := handler.NewRoomHandler(svc, discardLogger())

	body := `{"name":"Hall A","capacity":50}`
	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/rooms", strings.NewReader(body)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateRoom(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if got["id"] != "room-1" {
		t.Fatalf("got %v", got)
	}
}

func TestCreateRoom_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeRoomService{}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/rooms", strings.NewReader(`{}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateRoom(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRoom_NoActorInContext_Returns500(t *testing.T) {
	svc := &fakeRoomService{}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/rooms", strings.NewReader(`{"name":"Hall A"}`))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateRoom(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500 (wiring bug: Auth must run first)", rec.Code)
	}
}

func TestCreateRoom_DuplicateName_Returns409(t *testing.T) {
	svc := &fakeRoomService{createErr: domain.ErrConflict}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPost, "/v1/events/evt-1/rooms", strings.NewReader(`{"name":"Hall A"}`)))
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.CreateRoom(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRoom_Success(t *testing.T) {
	svc := &fakeRoomService{getOut: room.Room{ID: "room-1", Name: "Hall A"}}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/rooms/room-1", nil)
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.GetRoom(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRoom_NotFound_Returns404(t *testing.T) {
	svc := &fakeRoomService{getErr: domain.ErrNotFound}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/rooms/room-1", nil)
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.GetRoom(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestListRooms_Success_AppliesDefaultLimit(t *testing.T) {
	svc := &fakeRoomService{listOut: room.Page{Rooms: []room.Room{{ID: "room-1"}}}}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/rooms", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListRooms(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.gotLimit != 25 {
		t.Fatalf("got limit %d, want default 25", svc.gotLimit)
	}
	if svc.gotEventID != "evt-1" {
		t.Fatalf("got eventID %q, want evt-1", svc.gotEventID)
	}
}

func TestListRooms_ClampsLimitAboveMax(t *testing.T) {
	svc := &fakeRoomService{}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/rooms?limit=1000", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListRooms(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422 for a limit over the max", rec.Code)
	}
}

func TestListRooms_MalformedCursor_Returns400(t *testing.T) {
	svc := &fakeRoomService{}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/events/evt-1/rooms?cursor=not-a-real-cursor!!", nil)
	req.SetPathValue("eventId", "evt-1")
	rec := httptest.NewRecorder()

	h.ListRooms(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400 for a malformed cursor: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchRoom_Success(t *testing.T) {
	svc := &fakeRoomService{updateOut: room.Room{ID: "room-1", Name: "New name", Version: 2}}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/rooms/room-1", strings.NewReader(`{"version":1,"name":"New name"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.PatchRoom(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchRoom_InvalidBody_Returns422(t *testing.T) {
	svc := &fakeRoomService{}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/rooms/room-1", strings.NewReader(`{}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.PatchRoom(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchRoom_VersionMismatch_Returns409(t *testing.T) {
	svc := &fakeRoomService{updateErr: domain.ErrVersionMismatch}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/rooms/room-1", strings.NewReader(`{"version":1,"name":"New name"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.PatchRoom(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchRoom_NotFound_Returns404(t *testing.T) {
	svc := &fakeRoomService{updateErr: domain.ErrNotFound}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/rooms/room-1", strings.NewReader(`{"version":1,"name":"New name"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.PatchRoom(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestPatchRoom_NameCollision_Returns409WithRoomNameTakenCode proves
// PatchRoom's ErrConflict is distinguished from DeleteRoom's -- both share
// domain.ErrConflict, but a rename collision must surface as
// room_name_taken, not room_in_use.
func TestPatchRoom_NameCollision_Returns409WithRoomNameTakenCode(t *testing.T) {
	svc := &fakeRoomService{updateErr: domain.ErrConflict}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodPatch, "/v1/events/evt-1/rooms/room-1", strings.NewReader(`{"version":1,"name":"Taken"}`)))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.PatchRoom(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	errObj, _ := got["error"].(map[string]any)
	if errObj["code"] != "room_name_taken" {
		t.Fatalf("got error.code %v, want room_name_taken", errObj["code"])
	}
}

func TestDeleteRoom_Success(t *testing.T) {
	svc := &fakeRoomService{}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/rooms/room-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.DeleteRoom(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteRoom_MissingOrInvalidVersion_Returns422WithoutCallingService(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "version absent", path: "/v1/events/evt-1/rooms/room-1"},
		{name: "version empty", path: "/v1/events/evt-1/rooms/room-1?version="},
		{name: "version zero", path: "/v1/events/evt-1/rooms/room-1?version=0"},
		{name: "version negative", path: "/v1/events/evt-1/rooms/room-1?version=-1"},
		{name: "version non-numeric", path: "/v1/events/evt-1/rooms/room-1?version=abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeRoomService{}
			h := handler.NewRoomHandler(svc, discardLogger())

			req := withActor(httptest.NewRequest(http.MethodDelete, tc.path, nil))
			req.SetPathValue("eventId", "evt-1")
			req.SetPathValue("roomId", "room-1")
			rec := httptest.NewRecorder()

			h.DeleteRoom(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got status %d, want 422: %s", rec.Code, rec.Body.String())
			}
			if svc.deleteCalled {
				t.Fatal("service.DeleteRoom was called despite a missing/invalid version -- an unversioned delete must never reach the repository")
			}
		})
	}
}

func TestDeleteRoom_VersionMismatch_Returns409(t *testing.T) {
	svc := &fakeRoomService{deleteErr: domain.ErrVersionMismatch}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/rooms/room-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.DeleteRoom(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteRoom_NotFound_Returns404(t *testing.T) {
	svc := &fakeRoomService{deleteErr: domain.ErrNotFound}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/rooms/room-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.DeleteRoom(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestDeleteRoom_StillInUse_Returns409WithRoomInUseCode proves DeleteRoom's
// ErrConflict is distinguished from PatchRoom's -- the sessions-FK guard
// must surface as room_in_use, not room_name_taken.
func TestDeleteRoom_StillInUse_Returns409WithRoomInUseCode(t *testing.T) {
	svc := &fakeRoomService{deleteErr: domain.ErrConflict}
	h := handler.NewRoomHandler(svc, discardLogger())

	req := withActor(httptest.NewRequest(http.MethodDelete, "/v1/events/evt-1/rooms/room-1?version=1", nil))
	req.SetPathValue("eventId", "evt-1")
	req.SetPathValue("roomId", "room-1")
	rec := httptest.NewRecorder()

	h.DeleteRoom(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	errObj, _ := got["error"].(map[string]any)
	if errObj["code"] != "room_in_use" {
		t.Fatalf("got error.code %v, want room_in_use", errObj["code"])
	}
}
