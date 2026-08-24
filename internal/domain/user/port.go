package user

import "context"

// Repository is implemented by adapter/postgres. GetOrCreateBySubject must be
// an atomic upsert -- no check-then-act SELECT followed by a conditional
// INSERT -- since the subject unique constraint is what makes concurrent
// first-sign-ins from the same identity safe.
type Repository interface {
	GetOrCreateBySubject(ctx context.Context, subject, email, name string) (User, error)
}
