package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/opt"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

type fakeRepo struct {
	createActor string
	createEvent string
	createIn    session.CreateInput
	createOut   session.Session
	createErr   error

	getEvent   string
	getSession string
	getOut     session.Session
	getErr     error

	listEvent string
	listLimit int
	listAfter *session.Cursor
	listOut   session.Page
	listErr   error

	updateActor   string
	updateEvent   string
	updateSession string
	updateVer     int
	updatePatch   session.Patch
	updateOut     session.Session
	updateErr     error

	deleteActor   string
	deleteEvent   string
	deleteSession string
	deleteVer     int
	deleteOut     session.Session
	deleteErr     error

	seriesActor string
	seriesEvent string
	seriesIn    session.SeriesCreateInput
	seriesOut   session.Series
	seriesItems []session.SeriesOccurrenceResult
	seriesErr   error
}

func (f *fakeRepo) Create(ctx context.Context, actorID, eventID string, in session.CreateInput) (session.Session, error) {
	f.createActor, f.createEvent, f.createIn = actorID, eventID, in
	return f.createOut, f.createErr
}

func (f *fakeRepo) Get(ctx context.Context, eventID, sessionID string) (session.Session, error) {
	f.getEvent, f.getSession = eventID, sessionID
	return f.getOut, f.getErr
}

func (f *fakeRepo) List(ctx context.Context, eventID string, limit int, after *session.Cursor) (session.Page, error) {
	f.listEvent, f.listLimit, f.listAfter = eventID, limit, after
	return f.listOut, f.listErr
}

func (f *fakeRepo) Update(ctx context.Context, actorID, eventID, sessionID string, version int, patch session.Patch) (session.Session, error) {
	f.updateActor, f.updateEvent, f.updateSession, f.updateVer, f.updatePatch = actorID, eventID, sessionID, version, patch
	return f.updateOut, f.updateErr
}

func (f *fakeRepo) Delete(ctx context.Context, actorID, eventID, sessionID string, version int) (session.Session, error) {
	f.deleteActor, f.deleteEvent, f.deleteSession, f.deleteVer = actorID, eventID, sessionID, version
	return f.deleteOut, f.deleteErr
}

func (f *fakeRepo) CreateSeries(ctx context.Context, actorID, eventID string, in session.SeriesCreateInput) (session.Series, []session.SeriesOccurrenceResult, error) {
	f.seriesActor, f.seriesEvent, f.seriesIn = actorID, eventID, in
	return f.seriesOut, f.seriesItems, f.seriesErr
}

func TestService_CreateSession_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{createOut: session.Session{ID: "session-1"}}
	svc := session.NewService(repo)

	in := session.CreateInput{Title: "Keynote"}
	got, err := svc.CreateSession(context.Background(), "actor-1", "event-1", in)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.ID != "session-1" {
		t.Fatalf("got ID %q, want session-1", got.ID)
	}
	if repo.createActor != "actor-1" || repo.createEvent != "event-1" || repo.createIn.Title != "Keynote" {
		t.Fatalf("repo.Create called with wrong args: actor=%q event=%q in=%+v", repo.createActor, repo.createEvent, repo.createIn)
	}
}

func TestService_CreateSession_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{createErr: wantErr}
	svc := session.NewService(repo)

	_, err := svc.CreateSession(context.Background(), "actor-1", "event-1", session.CreateInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_GetSession_PassesThroughIDs(t *testing.T) {
	repo := &fakeRepo{getOut: session.Session{ID: "session-2"}}
	svc := session.NewService(repo)

	got, err := svc.GetSession(context.Background(), "event-1", "session-2")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != "session-2" || repo.getEvent != "event-1" || repo.getSession != "session-2" {
		t.Fatalf("got %+v, repo.getEvent=%q repo.getSession=%q", got, repo.getEvent, repo.getSession)
	}
}

func TestService_ListSessions_PassesThroughArgs(t *testing.T) {
	after := &session.Cursor{ID: "session-1"}
	repo := &fakeRepo{listOut: session.Page{Sessions: []session.Session{{ID: "session-3"}}}}
	svc := session.NewService(repo)

	got, err := svc.ListSessions(context.Background(), "event-1", 10, after)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "session-3" {
		t.Fatalf("got %+v", got)
	}
	if repo.listEvent != "event-1" || repo.listLimit != 10 || repo.listAfter != after {
		t.Fatalf("repo.List called with wrong args: event=%q limit=%d after=%v", repo.listEvent, repo.listLimit, repo.listAfter)
	}
}

func TestService_UpdateSession_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{updateOut: session.Session{ID: "session-4", Version: 2}}
	svc := session.NewService(repo)

	patch := session.Patch{Title: opt.Of("New Title")}
	got, err := svc.UpdateSession(context.Background(), "actor-1", "event-1", "session-4", 1, patch)
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("got Version %d, want 2", got.Version)
	}
	if repo.updateActor != "actor-1" || repo.updateEvent != "event-1" || repo.updateSession != "session-4" || repo.updateVer != 1 {
		t.Fatalf("repo.Update called with wrong args: actor=%q event=%q session=%q version=%d", repo.updateActor, repo.updateEvent, repo.updateSession, repo.updateVer)
	}
	if !repo.updatePatch.Title.Set || repo.updatePatch.Title.Value != "New Title" {
		t.Fatalf("repo.Update patch mismatch: %+v", repo.updatePatch)
	}
}

func TestService_UpdateSession_PropagatesVersionMismatch(t *testing.T) {
	wantErr := errors.New("version mismatch")
	repo := &fakeRepo{updateErr: wantErr}
	svc := session.NewService(repo)

	_, err := svc.UpdateSession(context.Background(), "actor-1", "event-1", "session-4", 1, session.Patch{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_DeleteSession_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{deleteOut: session.Session{ID: "session-5"}}
	svc := session.NewService(repo)

	_, err := svc.DeleteSession(context.Background(), "actor-1", "event-1", "session-5", 3)
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if repo.deleteActor != "actor-1" || repo.deleteEvent != "event-1" || repo.deleteSession != "session-5" || repo.deleteVer != 3 {
		t.Fatalf("repo.Delete called with wrong args: actor=%q event=%q session=%q version=%d", repo.deleteActor, repo.deleteEvent, repo.deleteSession, repo.deleteVer)
	}
}

func TestService_DeleteSession_PropagatesConflict(t *testing.T) {
	wantErr := errors.New("not found")
	repo := &fakeRepo{deleteErr: wantErr}
	svc := session.NewService(repo)

	_, err := svc.DeleteSession(context.Background(), "actor-1", "event-1", "session-5", 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_CreateSeries_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{
		seriesOut:   session.Series{ID: "series-1"},
		seriesItems: []session.SeriesOccurrenceResult{{Status: "created"}},
	}
	svc := session.NewService(repo)

	in := session.SeriesCreateInput{Title: "Standup", Freq: "daily", IntervalCount: 1, Occurrences: 5}
	series, items, err := svc.CreateSeries(context.Background(), "actor-1", "event-1", in)
	if err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	if series.ID != "series-1" || len(items) != 1 {
		t.Fatalf("got series=%+v items=%+v", series, items)
	}
	if repo.seriesActor != "actor-1" || repo.seriesEvent != "event-1" || repo.seriesIn.Title != "Standup" {
		t.Fatalf("repo.CreateSeries called with wrong args: actor=%q event=%q in=%+v", repo.seriesActor, repo.seriesEvent, repo.seriesIn)
	}
}

func TestService_CreateSeries_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{seriesErr: wantErr}
	svc := session.NewService(repo)

	_, _, err := svc.CreateSeries(context.Background(), "actor-1", "event-1", session.SeriesCreateInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}
