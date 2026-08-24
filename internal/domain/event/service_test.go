package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
)

type fakeRepo struct {
	createIn    event.CreateInput
	createActor string
	createOut   event.Event
	createErr   error
	getID       string
	getOut      event.Event
	getErr      error
	listUserID  string
	listLimit   int
	listAfter   *event.Cursor
	listOut     event.Page
	listErr     error
	updateActor string
	updateID    string
	updateVer   int
	updatePatch event.Patch
	updateOut   event.Event
	updateErr   error
	deleteActor string
	deleteID    string
	deleteVer   int
	deleteOut   event.Event
	deleteErr   error
}

func (f *fakeRepo) Create(ctx context.Context, actorID string, in event.CreateInput) (event.Event, error) {
	f.createActor, f.createIn = actorID, in
	return f.createOut, f.createErr
}

func (f *fakeRepo) Get(ctx context.Context, id string) (event.Event, error) {
	f.getID = id
	return f.getOut, f.getErr
}

func (f *fakeRepo) List(ctx context.Context, userID string, limit int, after *event.Cursor) (event.Page, error) {
	f.listUserID, f.listLimit, f.listAfter = userID, limit, after
	return f.listOut, f.listErr
}

func (f *fakeRepo) Update(ctx context.Context, actorID, id string, version int, patch event.Patch) (event.Event, error) {
	f.updateActor, f.updateID, f.updateVer, f.updatePatch = actorID, id, version, patch
	return f.updateOut, f.updateErr
}

func (f *fakeRepo) Delete(ctx context.Context, actorID, id string, version int) (event.Event, error) {
	f.deleteActor, f.deleteID, f.deleteVer = actorID, id, version
	return f.deleteOut, f.deleteErr
}

func TestService_CreateEvent_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{createOut: event.Event{ID: "evt-1"}}
	svc := event.NewService(repo)

	in := event.CreateInput{
		Name:     "Conf",
		Timezone: "Asia/Colombo",
		StartsAt: time.Now(),
		EndsAt:   time.Now().Add(time.Hour),
	}

	got, err := svc.CreateEvent(context.Background(), "actor-1", in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if got.ID != "evt-1" {
		t.Fatalf("got ID %q, want evt-1", got.ID)
	}
	if repo.createActor != "actor-1" || repo.createIn.Name != "Conf" {
		t.Fatalf("repo.Create called with wrong args: actor=%q in=%+v", repo.createActor, repo.createIn)
	}
}

func TestService_CreateEvent_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{createErr: wantErr}
	svc := event.NewService(repo)

	_, err := svc.CreateEvent(context.Background(), "actor-1", event.CreateInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_GetEvent_PassesThroughID(t *testing.T) {
	repo := &fakeRepo{getOut: event.Event{ID: "evt-2"}}
	svc := event.NewService(repo)

	got, err := svc.GetEvent(context.Background(), "evt-2")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.ID != "evt-2" || repo.getID != "evt-2" {
		t.Fatalf("got %+v, repo.getID=%q", got, repo.getID)
	}
}

func TestService_ListEvents_PassesThroughArgs(t *testing.T) {
	after := &event.Cursor{ID: "evt-1"}
	repo := &fakeRepo{listOut: event.Page{Events: []event.Event{{ID: "evt-3"}}}}
	svc := event.NewService(repo)

	got, err := svc.ListEvents(context.Background(), "user-1", 10, after)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].ID != "evt-3" {
		t.Fatalf("got %+v", got)
	}
	if repo.listUserID != "user-1" || repo.listLimit != 10 || repo.listAfter != after {
		t.Fatalf("repo.List called with wrong args: userID=%q limit=%d after=%v", repo.listUserID, repo.listLimit, repo.listAfter)
	}
}

func TestService_UpdateEvent_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{updateOut: event.Event{ID: "evt-4", Version: 2}}
	svc := event.NewService(repo)

	patch := event.Patch{Name: opt.Of("New name")}
	got, err := svc.UpdateEvent(context.Background(), "actor-1", "evt-4", 1, patch)
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("got Version %d, want 2", got.Version)
	}
	if repo.updateActor != "actor-1" || repo.updateID != "evt-4" || repo.updateVer != 1 {
		t.Fatalf("repo.Update called with wrong args: actor=%q id=%q version=%d", repo.updateActor, repo.updateID, repo.updateVer)
	}
	if !repo.updatePatch.Name.Set || repo.updatePatch.Name.Value != "New name" {
		t.Fatalf("repo.Update patch mismatch: %+v", repo.updatePatch)
	}
}

func TestService_UpdateEvent_PropagatesVersionMismatch(t *testing.T) {
	wantErr := errors.New("version mismatch")
	repo := &fakeRepo{updateErr: wantErr}
	svc := event.NewService(repo)

	_, err := svc.UpdateEvent(context.Background(), "actor-1", "evt-4", 1, event.Patch{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_DeleteEvent_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{deleteOut: event.Event{ID: "evt-5"}}
	svc := event.NewService(repo)

	got, err := svc.DeleteEvent(context.Background(), "actor-1", "evt-5", 3)
	if err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
	if got.ID != "evt-5" {
		t.Fatalf("got %+v", got)
	}
	if repo.deleteActor != "actor-1" || repo.deleteID != "evt-5" || repo.deleteVer != 3 {
		t.Fatalf("repo.Delete called with wrong args: actor=%q id=%q version=%d", repo.deleteActor, repo.deleteID, repo.deleteVer)
	}
}
