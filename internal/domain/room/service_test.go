package room_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
)

type fakeRepo struct {
	createActor string
	createEvent string
	createIn    room.CreateInput
	createOut   room.Room
	createErr   error

	getEvent string
	getRoom  string
	getOut   room.Room
	getErr   error

	listEvent string
	listLimit int
	listAfter *room.Cursor
	listOut   room.Page
	listErr   error

	updateActor string
	updateEvent string
	updateRoom  string
	updateVer   int
	updatePatch room.Patch
	updateOut   room.Room
	updateErr   error

	deleteActor string
	deleteEvent string
	deleteRoom  string
	deleteVer   int
	deleteErr   error
}

func (f *fakeRepo) Create(ctx context.Context, actorID, eventID string, in room.CreateInput) (room.Room, error) {
	f.createActor, f.createEvent, f.createIn = actorID, eventID, in
	return f.createOut, f.createErr
}

func (f *fakeRepo) Get(ctx context.Context, eventID, roomID string) (room.Room, error) {
	f.getEvent, f.getRoom = eventID, roomID
	return f.getOut, f.getErr
}

func (f *fakeRepo) List(ctx context.Context, eventID string, limit int, after *room.Cursor) (room.Page, error) {
	f.listEvent, f.listLimit, f.listAfter = eventID, limit, after
	return f.listOut, f.listErr
}

func (f *fakeRepo) Update(ctx context.Context, actorID, eventID, roomID string, version int, patch room.Patch) (room.Room, error) {
	f.updateActor, f.updateEvent, f.updateRoom, f.updateVer, f.updatePatch = actorID, eventID, roomID, version, patch
	return f.updateOut, f.updateErr
}

func (f *fakeRepo) Delete(ctx context.Context, actorID, eventID, roomID string, version int) error {
	f.deleteActor, f.deleteEvent, f.deleteRoom, f.deleteVer = actorID, eventID, roomID, version
	return f.deleteErr
}

func TestService_CreateRoom_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{createOut: room.Room{ID: "room-1"}}
	svc := room.NewService(repo)

	in := room.CreateInput{Name: "Hall A"}
	got, err := svc.CreateRoom(context.Background(), "actor-1", "event-1", in)
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if got.ID != "room-1" {
		t.Fatalf("got ID %q, want room-1", got.ID)
	}
	if repo.createActor != "actor-1" || repo.createEvent != "event-1" || repo.createIn.Name != "Hall A" {
		t.Fatalf("repo.Create called with wrong args: actor=%q event=%q in=%+v", repo.createActor, repo.createEvent, repo.createIn)
	}
}

func TestService_CreateRoom_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{createErr: wantErr}
	svc := room.NewService(repo)

	_, err := svc.CreateRoom(context.Background(), "actor-1", "event-1", room.CreateInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_GetRoom_PassesThroughIDs(t *testing.T) {
	repo := &fakeRepo{getOut: room.Room{ID: "room-2"}}
	svc := room.NewService(repo)

	got, err := svc.GetRoom(context.Background(), "event-1", "room-2")
	if err != nil {
		t.Fatalf("GetRoom: %v", err)
	}
	if got.ID != "room-2" || repo.getEvent != "event-1" || repo.getRoom != "room-2" {
		t.Fatalf("got %+v, repo.getEvent=%q repo.getRoom=%q", got, repo.getEvent, repo.getRoom)
	}
}

func TestService_ListRooms_PassesThroughArgs(t *testing.T) {
	after := &room.Cursor{ID: "room-1"}
	repo := &fakeRepo{listOut: room.Page{Rooms: []room.Room{{ID: "room-3"}}}}
	svc := room.NewService(repo)

	got, err := svc.ListRooms(context.Background(), "event-1", 10, after)
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(got.Rooms) != 1 || got.Rooms[0].ID != "room-3" {
		t.Fatalf("got %+v", got)
	}
	if repo.listEvent != "event-1" || repo.listLimit != 10 || repo.listAfter != after {
		t.Fatalf("repo.List called with wrong args: event=%q limit=%d after=%v", repo.listEvent, repo.listLimit, repo.listAfter)
	}
}

func TestService_UpdateRoom_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{updateOut: room.Room{ID: "room-4", Version: 2}}
	svc := room.NewService(repo)

	patch := room.Patch{Name: opt.Of("Hall B")}
	got, err := svc.UpdateRoom(context.Background(), "actor-1", "event-1", "room-4", 1, patch)
	if err != nil {
		t.Fatalf("UpdateRoom: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("got Version %d, want 2", got.Version)
	}
	if repo.updateActor != "actor-1" || repo.updateEvent != "event-1" || repo.updateRoom != "room-4" || repo.updateVer != 1 {
		t.Fatalf("repo.Update called with wrong args: actor=%q event=%q room=%q version=%d", repo.updateActor, repo.updateEvent, repo.updateRoom, repo.updateVer)
	}
	if !repo.updatePatch.Name.Set || repo.updatePatch.Name.Value != "Hall B" {
		t.Fatalf("repo.Update patch mismatch: %+v", repo.updatePatch)
	}
}

func TestService_UpdateRoom_PropagatesVersionMismatch(t *testing.T) {
	wantErr := errors.New("version mismatch")
	repo := &fakeRepo{updateErr: wantErr}
	svc := room.NewService(repo)

	_, err := svc.UpdateRoom(context.Background(), "actor-1", "event-1", "room-4", 1, room.Patch{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_DeleteRoom_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{}
	svc := room.NewService(repo)

	err := svc.DeleteRoom(context.Background(), "actor-1", "event-1", "room-5", 3)
	if err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}
	if repo.deleteActor != "actor-1" || repo.deleteEvent != "event-1" || repo.deleteRoom != "room-5" || repo.deleteVer != 3 {
		t.Fatalf("repo.Delete called with wrong args: actor=%q event=%q room=%q version=%d", repo.deleteActor, repo.deleteEvent, repo.deleteRoom, repo.deleteVer)
	}
}

func TestService_DeleteRoom_PropagatesConflict(t *testing.T) {
	wantErr := errors.New("still in use")
	repo := &fakeRepo{deleteErr: wantErr}
	svc := room.NewService(repo)

	err := svc.DeleteRoom(context.Background(), "actor-1", "event-1", "room-5", 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}
