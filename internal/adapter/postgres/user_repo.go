// Package postgres implements domain repository ports against real
// Postgres via pgx. It maps constraint violations to domain errors and
// never lets a check-then-act race leak into a query.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
)

// UserRepository implements domain/user.Repository.
type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// GetOrCreateBySubject is an atomic upsert, never a check-then-act: the
// INSERT ... ON CONFLICT DO NOTHING is the one decision point. If it
// inserted nothing, the row already existed and the follow-up SELECT only
// fetches what's there -- it doesn't decide anything, so there's no race
// window. email/name are set once, at creation, and never overwritten on a
// later sign-in (see the feature's decision on this).
func (r *UserRepository) GetOrCreateBySubject(ctx context.Context, subject, email, name string) (user.User, error) {
	const insert = `
		INSERT INTO users (subject, email, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (subject) DO NOTHING
		RETURNING id::text, subject, email, name, created_at, updated_at`

	u, err := scanUser(r.pool.QueryRow(ctx, insert, subject, email, name))
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, fmt.Errorf("inserting user: %w", err)
	}

	const selectExisting = `
		SELECT id::text, subject, email, name, created_at, updated_at
		FROM users
		WHERE subject = $1`

	u, err = scanUser(r.pool.QueryRow(ctx, selectExisting, subject))
	if err != nil {
		return user.User{}, fmt.Errorf("selecting existing user: %w", err)
	}
	return u, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (user.User, error) {
	var u user.User
	err := row.Scan(&u.ID, &u.Subject, &u.Email, &u.Name, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
