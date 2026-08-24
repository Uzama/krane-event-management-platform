package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
)

type fakeRepo struct {
	got struct {
		subject, email, name string
	}
	ret user.User
	err error
}

func (f *fakeRepo) GetOrCreateBySubject(_ context.Context, subject, email, name string) (user.User, error) {
	f.got.subject, f.got.email, f.got.name = subject, email, name
	return f.ret, f.err
}

func TestService_GetOrCreateBySubject_DelegatesAndReturnsRepoResult(t *testing.T) {
	want := user.User{ID: "u-1", Subject: "sub-123", Email: "a@b.com", Name: "A B"}
	repo := &fakeRepo{ret: want}
	svc := user.NewService(repo)

	got, err := svc.GetOrCreateBySubject(context.Background(), "sub-123", "a@b.com", "A B")
	if err != nil {
		t.Fatalf("GetOrCreateBySubject: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if repo.got.subject != "sub-123" || repo.got.email != "a@b.com" || repo.got.name != "A B" {
		t.Errorf("repo received %+v, want subject/email/name passed through unchanged", repo.got)
	}
}

func TestService_GetOrCreateBySubject_PropagatesRepoError(t *testing.T) {
	wantErr := errors.New("boom")
	repo := &fakeRepo{err: wantErr}
	svc := user.NewService(repo)

	_, err := svc.GetOrCreateBySubject(context.Background(), "sub-123", "a@b.com", "A B")
	if !errors.Is(err, wantErr) {
		t.Errorf("got err %v, want %v", err, wantErr)
	}
}
