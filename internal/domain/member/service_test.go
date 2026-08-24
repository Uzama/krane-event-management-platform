package member_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
)

type fakeRepo struct {
	createActor, createEvent string
	createIn                 member.CreateInput
	createOut                member.Member
	createErr                error

	getEvent, getMember string
	getOut              member.Member
	getErr              error

	listEvent string
	listLimit int
	listAfter *member.Cursor
	listOut   member.Page
	listErr   error

	assignActor, assignEvent, assignMember string
	assignVersion                          int
	assignRole                             string
	assignOut                              member.Member
	assignErr                              error

	deleteActor, deleteEvent, deleteMember string
	deleteVersion                          int
	deleteErr                              error
}

func (f *fakeRepo) Create(ctx context.Context, actorID, eventID string, in member.CreateInput) (member.Member, error) {
	f.createActor, f.createEvent, f.createIn = actorID, eventID, in
	return f.createOut, f.createErr
}

func (f *fakeRepo) Get(ctx context.Context, eventID, memberID string) (member.Member, error) {
	f.getEvent, f.getMember = eventID, memberID
	return f.getOut, f.getErr
}

func (f *fakeRepo) List(ctx context.Context, eventID string, limit int, after *member.Cursor) (member.Page, error) {
	f.listEvent, f.listLimit, f.listAfter = eventID, limit, after
	return f.listOut, f.listErr
}

func (f *fakeRepo) AssignRole(ctx context.Context, actorID, eventID, memberID string, version int, role string) (member.Member, error) {
	f.assignActor, f.assignEvent, f.assignMember, f.assignVersion, f.assignRole = actorID, eventID, memberID, version, role
	return f.assignOut, f.assignErr
}

func (f *fakeRepo) Delete(ctx context.Context, actorID, eventID, memberID string, version int) error {
	f.deleteActor, f.deleteEvent, f.deleteMember, f.deleteVersion = actorID, eventID, memberID, version
	return f.deleteErr
}

func TestService_CreateMember_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{createOut: member.Member{ID: "mem-1"}}
	svc := member.NewService(repo)

	in := member.CreateInput{Email: "person@example.com", Role: "attendee"}
	got, err := svc.CreateMember(context.Background(), "actor-1", "evt-1", in)
	if err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if got.ID != "mem-1" {
		t.Fatalf("got ID %q, want mem-1", got.ID)
	}
	if repo.createActor != "actor-1" || repo.createEvent != "evt-1" || repo.createIn.Email != "person@example.com" {
		t.Fatalf("repo.Create called with wrong args: actor=%q event=%q in=%+v", repo.createActor, repo.createEvent, repo.createIn)
	}
}

func TestService_CreateMember_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{createErr: wantErr}
	svc := member.NewService(repo)

	_, err := svc.CreateMember(context.Background(), "actor-1", "evt-1", member.CreateInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_ListMembers_PassesThroughArgs(t *testing.T) {
	after := &member.Cursor{ID: "mem-1"}
	repo := &fakeRepo{listOut: member.Page{Members: []member.Member{{ID: "mem-2"}}}}
	svc := member.NewService(repo)

	got, err := svc.ListMembers(context.Background(), "evt-1", 10, after)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(got.Members) != 1 || got.Members[0].ID != "mem-2" {
		t.Fatalf("got %+v", got)
	}
	if repo.listEvent != "evt-1" || repo.listLimit != 10 || repo.listAfter != after {
		t.Fatalf("repo.List called with wrong args: event=%q limit=%d after=%v", repo.listEvent, repo.listLimit, repo.listAfter)
	}
}

func TestService_AssignRole_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{assignOut: member.Member{ID: "mem-3", Role: "contributor", Version: 2, UpdatedAt: time.Now()}}
	svc := member.NewService(repo)

	got, err := svc.AssignRole(context.Background(), "actor-1", "evt-1", "mem-3", 1, "contributor")
	if err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if got.Role != "contributor" {
		t.Fatalf("got Role %q, want contributor", got.Role)
	}
	if repo.assignActor != "actor-1" || repo.assignEvent != "evt-1" || repo.assignMember != "mem-3" || repo.assignVersion != 1 || repo.assignRole != "contributor" {
		t.Fatalf("repo.AssignRole called with wrong args: %+v", repo)
	}
}

func TestService_AssignRole_PropagatesConflict(t *testing.T) {
	wantErr := errors.New("last admin")
	repo := &fakeRepo{assignErr: wantErr}
	svc := member.NewService(repo)

	_, err := svc.AssignRole(context.Background(), "actor-1", "evt-1", "mem-3", 1, "contributor")
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_RemoveMember_PassesThroughArgs(t *testing.T) {
	repo := &fakeRepo{}
	svc := member.NewService(repo)

	if err := svc.RemoveMember(context.Background(), "actor-1", "evt-1", "mem-4", 3); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if repo.deleteActor != "actor-1" || repo.deleteEvent != "evt-1" || repo.deleteMember != "mem-4" || repo.deleteVersion != 3 {
		t.Fatalf("repo.Delete called with wrong args: %+v", repo)
	}
}

func TestService_RemoveMember_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{deleteErr: wantErr}
	svc := member.NewService(repo)

	err := svc.RemoveMember(context.Background(), "actor-1", "evt-1", "mem-4", 3)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}
