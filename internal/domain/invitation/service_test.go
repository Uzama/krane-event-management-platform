package invitation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
)

type fakeRepo struct {
	createActor, createEvent string
	createIn                 invitation.CreateInput
	createOut                invitation.Invitation
	createErr                error

	listEvent string
	listLimit int
	listAfter *invitation.Cursor
	listOut   invitation.Page
	listErr   error
}

func (f *fakeRepo) Create(ctx context.Context, actorID, eventID string, in invitation.CreateInput) (invitation.Invitation, error) {
	f.createActor, f.createEvent, f.createIn = actorID, eventID, in
	return f.createOut, f.createErr
}

func (f *fakeRepo) List(ctx context.Context, eventID string, limit int, after *invitation.Cursor) (invitation.Page, error) {
	f.listEvent, f.listLimit, f.listAfter = eventID, limit, after
	return f.listOut, f.listErr
}

func TestService_CreateInvitation_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{createOut: invitation.Invitation{ID: "inv-1"}}
	svc := invitation.NewService(repo)

	in := invitation.CreateInput{Email: "person@example.com", Role: "attendee"}
	got, err := svc.CreateInvitation(context.Background(), "actor-1", "evt-1", in)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	if got.ID != "inv-1" {
		t.Fatalf("got ID %q, want inv-1", got.ID)
	}
	if repo.createActor != "actor-1" || repo.createEvent != "evt-1" || repo.createIn.Email != "person@example.com" {
		t.Fatalf("repo.Create called with wrong args: actor=%q event=%q in=%+v", repo.createActor, repo.createEvent, repo.createIn)
	}
}

func TestService_CreateInvitation_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{createErr: wantErr}
	svc := invitation.NewService(repo)

	_, err := svc.CreateInvitation(context.Background(), "actor-1", "evt-1", invitation.CreateInput{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestService_ListInvitations_PassesThroughArgs(t *testing.T) {
	after := &invitation.Cursor{ID: "inv-1"}
	repo := &fakeRepo{listOut: invitation.Page{Invitations: []invitation.Invitation{{ID: "inv-2"}}}}
	svc := invitation.NewService(repo)

	got, err := svc.ListInvitations(context.Background(), "evt-1", 10, after)
	if err != nil {
		t.Fatalf("ListInvitations: %v", err)
	}
	if len(got.Invitations) != 1 || got.Invitations[0].ID != "inv-2" {
		t.Fatalf("got %+v", got)
	}
	if repo.listEvent != "evt-1" || repo.listLimit != 10 || repo.listAfter != after {
		t.Fatalf("repo.List called with wrong args: event=%q limit=%d after=%v", repo.listEvent, repo.listLimit, repo.listAfter)
	}
}
