package invitation

import "context"

// Service is the use-case layer for the invitation aggregate. It is a thin
// pass-through: every invariant this feature enforces (email resolution,
// the invite-role privilege guard, audit atomicity) lives in the
// repository's own transaction, matching domain/member.Service's
// precedent.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateInvitation(ctx context.Context, actorID, eventID string, in CreateInput) (Invitation, error) {
	return s.repo.Create(ctx, actorID, eventID, in)
}

func (s *Service) ListInvitations(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error) {
	return s.repo.List(ctx, eventID, limit, after)
}
