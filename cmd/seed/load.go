package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// Truncate clears every table this generator owns, in FK-safe order, so
// `make seed` is idempotent -- a re-run against an already-seeded database
// produces a clean re-seed, not a unique-constraint error (feature plan
// decision 5). It runs as krane_migrator (see cmd/seed/main.go), which is
// what makes two things possible that krane_app can't do at all:
//
//  1. idempotency_keys and audit_log both have `actor_id REFERENCES
//     users(id)` with no ON DELETE CASCADE, so DELETE FROM users fails
//     outright if either table still references a user being removed. The
//     whole point of seeding the 3 demo identities (decision 4) is that
//     `make token USER=admin` hits the *real* API and writes a *real*
//     audit_log row -- so this isn't a hypothetical, it's the expected
//     result of using the very feature seed exists to demo.
//  2. krane_app has no DELETE grant on audit_log at all (item 02,
//     append-only) -- it could never clear that row, full stop.
//
// The audit_log delete is deliberately SCOPED to `WHERE actor_id IN (SELECT
// id FROM users)` -- rows whose actor is a user this exact run is about to
// delete and regenerate -- not a blanket wipe. Real audit history from any
// other actor is left untouched; in practice the seeded demo identities are
// the only subjects that can ever hold a real token outside Go tests, so
// this clears exactly their demo-activity trail and nothing else.
// role_permissions is never touched here (read-only invariant, item 07).
func Truncate(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`DELETE FROM idempotency_keys`,
		`DELETE FROM audit_log WHERE actor_id IN (SELECT id FROM users)`,
		`DELETE FROM invitations`,
		`DELETE FROM sessions`,
		`DELETE FROM event_members`,
		`DELETE FROM rooms`,
		`DELETE FROM events`,
		`DELETE FROM users`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("truncate: %s: %w", stmt, err)
		}
	}
	return nil
}

// Load bulk-inserts a generated Dataset in FK-safe order: users, events,
// event_members, rooms, sessions, invitations. Every row's id was already
// assigned client-side by generate.go (newUUIDv7), which is what lets rooms
// reference their event, sessions reference their room/speaker, and so on,
// all in one pass with no RETURNING round-trip needed.
//
// Every table except sessions loads via pgx.CopyFrom (COPY FROM STDIN
// BINARY) -- the fastest bulk-load path pgx offers, appropriate at this
// item's scale (50,000 invitations). sessions is the one exception: its
// time_range column is a tstzrange, and the repositories already establish
// the half-open convention as `tstzrange($1, $2, '[)')` at the SQL level
// (internal/adapter/postgres/session_repo.go) -- reused here via a batched
// multi-row INSERT rather than reimplementing tstzrange's binary wire
// format by hand.
func Load(ctx context.Context, pool *pgxpool.Pool, ds Dataset) error {
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"users"},
		[]string{"id", "subject", "email", "name", "created_at", "updated_at"},
		pgx.CopyFromSlice(len(ds.Users), func(i int) ([]any, error) {
			u := ds.Users[i]
			return []any{u.ID, u.Subject, u.Email, u.Name, u.CreatedAt, u.UpdatedAt}, nil
		}),
	); err != nil {
		return fmt.Errorf("loading users: %w", err)
	}

	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"events"},
		[]string{"id", "name", "description", "timezone", "starts_at", "ends_at", "version", "created_at", "updated_at"},
		pgx.CopyFromSlice(len(ds.Events), func(i int) ([]any, error) {
			e := ds.Events[i]
			return []any{e.ID, e.Name, e.Description, e.Timezone, e.StartsAt, e.EndsAt, e.Version, e.CreatedAt, e.UpdatedAt}, nil
		}),
	); err != nil {
		return fmt.Errorf("loading events: %w", err)
	}

	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"event_members"},
		[]string{"id", "event_id", "user_id", "role", "version", "created_at", "updated_at"},
		pgx.CopyFromSlice(len(ds.Members), func(i int) ([]any, error) {
			m := ds.Members[i]
			return []any{m.ID, m.EventID, m.UserID, m.Role, m.Version, m.CreatedAt, m.UpdatedAt}, nil
		}),
	); err != nil {
		return fmt.Errorf("loading event_members: %w", err)
	}

	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"rooms"},
		[]string{"id", "event_id", "name", "capacity", "version", "created_at", "updated_at"},
		pgx.CopyFromSlice(len(ds.Rooms), func(i int) ([]any, error) {
			r := ds.Rooms[i]
			return []any{r.ID, r.EventID, r.Name, r.Capacity, r.Version, r.CreatedAt, r.UpdatedAt}, nil
		}),
	); err != nil {
		return fmt.Errorf("loading rooms: %w", err)
	}

	if err := loadSessions(ctx, pool, ds.Sessions); err != nil {
		return fmt.Errorf("loading sessions: %w", err)
	}

	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"invitations"},
		[]string{"id", "event_id", "user_id", "email", "role", "created_at", "updated_at"},
		pgx.CopyFromSlice(len(ds.Invitations), func(i int) ([]any, error) {
			inv := ds.Invitations[i]
			return []any{inv.ID, inv.EventID, inv.UserID, inv.Email, inv.Role, inv.CreatedAt, inv.UpdatedAt}, nil
		}),
	); err != nil {
		return fmt.Errorf("loading invitations: %w", err)
	}

	return nil
}

// sessionChunkSize keeps each INSERT's parameter count (11 per row) well
// under Postgres' 65535 limit while still batching thousands of rows into a
// handful of round trips.
const sessionChunkSize = 1000

func loadSessions(ctx context.Context, pool *pgxpool.Pool, sessions []session.Session) error {
	for start := 0; start < len(sessions); start += sessionChunkSize {
		end := start + sessionChunkSize
		if end > len(sessions) {
			end = len(sessions)
		}
		if err := loadSessionsChunk(ctx, pool, sessions[start:end]); err != nil {
			return fmt.Errorf("chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

func loadSessionsChunk(ctx context.Context, pool *pgxpool.Pool, chunk []session.Session) error {
	if len(chunk) == 0 {
		return nil
	}

	var sql strings.Builder
	sql.WriteString(`INSERT INTO sessions (id, event_id, room_id, speaker_id, title, description, time_range, version, created_at, updated_at) VALUES `)
	args := make([]any, 0, len(chunk)*11)
	for i, s := range chunk {
		if i > 0 {
			sql.WriteString(",")
		}
		base := i * 11
		fmt.Fprintf(&sql, "($%d,$%d,$%d,$%d,$%d,$%d,tstzrange($%d,$%d,'[)'),$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11)
		args = append(args, s.ID, s.EventID, s.RoomID, s.SpeakerID, s.Title, s.Description, s.StartsAt, s.EndsAt, s.Version, s.CreatedAt, s.UpdatedAt)
	}

	if _, err := pool.Exec(ctx, sql.String(), args...); err != nil {
		return err
	}
	return nil
}
