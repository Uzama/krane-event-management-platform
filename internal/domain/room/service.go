package room

import "context"

// Service is the use-case layer for the room aggregate. It is a thin
// pass-through: every invariant this feature enforces (uniqueness,
// version-checked writes, audit atomicity, the sessions-FK delete guard)
// lives in the repository's own transaction, matching
// domain/event.Service's precedent.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateRoom(ctx context.Context, actorID, eventID string, in CreateInput) (Room, error) {
	return s.repo.Create(ctx, actorID, eventID, in)
}

func (s *Service) GetRoom(ctx context.Context, eventID, roomID string) (Room, error) {
	return s.repo.Get(ctx, eventID, roomID)
}

func (s *Service) ListRooms(ctx context.Context, eventID string, limit int, after *Cursor) (Page, error) {
	return s.repo.List(ctx, eventID, limit, after)
}

func (s *Service) UpdateRoom(ctx context.Context, actorID, eventID, roomID string, version int, patch Patch) (Room, error) {
	return s.repo.Update(ctx, actorID, eventID, roomID, version, patch)
}

func (s *Service) DeleteRoom(ctx context.Context, actorID, eventID, roomID string, version int) error {
	return s.repo.Delete(ctx, actorID, eventID, roomID, version)
}
