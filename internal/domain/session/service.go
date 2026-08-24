package session

import "context"

// Service is the use-case layer for the session aggregate. It is a thin
// pass-through: every invariant this feature enforces (room/event/speaker
// referential integrity, version-checked writes, audit atomicity) lives in
// the repository's own transaction, matching domain/room.Service's
// precedent.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateSession(ctx context.Context, actorID, eventID string, in CreateInput) (Session, error) {
	return s.repo.Create(ctx, actorID, eventID, in)
}

func (s *Service) GetSession(ctx context.Context, eventID, sessionID string) (Session, error) {
	return s.repo.Get(ctx, eventID, sessionID)
}

func (s *Service) ListSessions(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error) {
	return s.repo.List(ctx, eventID, limit, after)
}

func (s *Service) UpdateSession(ctx context.Context, actorID, eventID, sessionID string, version int, patch Patch) (Session, error) {
	return s.repo.Update(ctx, actorID, eventID, sessionID, version, patch)
}

func (s *Service) DeleteSession(ctx context.Context, actorID, eventID, sessionID string, version int) (Session, error) {
	return s.repo.Delete(ctx, actorID, eventID, sessionID, version)
}

func (s *Service) CreateSeries(ctx context.Context, actorID, eventID string, in SeriesCreateInput) (Series, []SeriesOccurrenceResult, error) {
	return s.repo.CreateSeries(ctx, actorID, eventID, in)
}
