package member

import "context"

// Service is the use-case layer for the member aggregate. It is a thin
// pass-through: every invariant this feature enforces (email resolution,
// role-grant privilege, last-admin protection, version-gating, audit
// atomicity) lives in the repository's own transaction, matching
// domain/event.Service's precedent.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateMember(ctx context.Context, actorID, eventID string, in CreateInput) (Member, error) {
	return s.repo.Create(ctx, actorID, eventID, in)
}

func (s *Service) ListMembers(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error) {
	return s.repo.List(ctx, eventID, limit, after)
}

func (s *Service) AssignRole(ctx context.Context, actorID, eventID, memberID string, version int, role string) (Member, error) {
	return s.repo.AssignRole(ctx, actorID, eventID, memberID, version, role)
}

func (s *Service) RemoveMember(ctx context.Context, actorID, eventID, memberID string, version int) error {
	return s.repo.Delete(ctx, actorID, eventID, memberID, version)
}
