package user

import "context"

// Service is the use-case layer for the user aggregate.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetOrCreateBySubject maps a validated token's identity claims to a users
// row, creating it on first sign-in. The atomicity guarantee lives in
// Postgres (the repository's upsert), not here -- this method is a thin
// pass-through so the auth middleware depends on domain, never on adapter.
func (s *Service) GetOrCreateBySubject(ctx context.Context, subject, email, name string) (User, error) {
	return s.repo.GetOrCreateBySubject(ctx, subject, email, name)
}
