package event

import "context"

// Service is the use-case layer for the event aggregate. It is a thin
// pass-through: every invariant this feature enforces (ends-after-starts,
// version-checked writes, audit atomicity) lives either at the
// http/request boundary or in the repository's own transaction, matching
// domain/user.Service's precedent -- the atomicity guarantee belongs to
// Postgres, not application code.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateEvent(ctx context.Context, actorID string, in CreateInput) (Event, error) {
	return s.repo.Create(ctx, actorID, in)
}

func (s *Service) GetEvent(ctx context.Context, id string) (Event, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) ListEvents(ctx context.Context, userID string, limit int, after *Cursor) (Page, error) {
	return s.repo.List(ctx, userID, limit, after)
}

func (s *Service) UpdateEvent(ctx context.Context, actorID, id string, version int, patch Patch) (Event, error) {
	return s.repo.Update(ctx, actorID, id, version, patch)
}

func (s *Service) DeleteEvent(ctx context.Context, actorID, id string, version int) (Event, error) {
	return s.repo.Delete(ctx, actorID, id, version)
}
