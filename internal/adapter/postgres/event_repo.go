package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
)

var dialect = goqu.Dialect("postgres")

// EventRepository implements domain/event.Repository.
type EventRepository struct {
	pool *pgxpool.Pool
}

func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

// eventRow mirrors the events table's JSON shape -- to_jsonb(events) keys
// match column names exactly, so this is what audit_log's before/after and
// the create/update RETURNING payloads decode into.
type eventRow struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	Timezone    string     `json:"timezone"`
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      time.Time  `json:"ends_at"`
	Version     int        `json:"version"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (r eventRow) toEvent() event.Event {
	return event.Event{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Timezone:    r.Timezone,
		StartsAt:    r.StartsAt,
		EndsAt:      r.EndsAt,
		Version:     r.Version,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// memberRow mirrors event_members' JSON shape, for the auto-admin-grant's
// own audit "after".
type memberRow struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Create inserts the event and, in the same transaction, grants actorID an
// admin event_members row and audits both -- role_permissions has no
// event:create row (item 07), so this is the only way the creator ever
// gains standing to read what they just made. One CTE statement does both
// inserts atomically; two more statements write the audit rows before
// commit -- a failure anywhere rolls every one of them back together.
func (r *EventRepository) Create(ctx context.Context, actorID string, in event.CreateInput) (event.Event, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return event.Event{}, fmt.Errorf("beginning create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		WITH new_event AS (
			INSERT INTO events (name, description, timezone, starts_at, ends_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, name, description, timezone, starts_at, ends_at, version, deleted_at, created_at, updated_at
		), new_member AS (
			INSERT INTO event_members (event_id, user_id, role)
			SELECT id, $6, 'admin' FROM new_event
			RETURNING id, event_id, user_id, role, created_at, updated_at
		)
		SELECT
			(SELECT row_to_json(new_event) FROM new_event),
			(SELECT row_to_json(new_member) FROM new_member)`

	var eventJSON, memberJSON []byte
	if err := tx.QueryRow(ctx, insert, in.Name, in.Description, in.Timezone, in.StartsAt, in.EndsAt, actorID).
		Scan(&eventJSON, &memberJSON); err != nil {
		return event.Event{}, fmt.Errorf("creating event: %w", err)
	}

	var row eventRow
	if err := json.Unmarshal(eventJSON, &row); err != nil {
		return event.Event{}, fmt.Errorf("decoding created event: %w", err)
	}
	var member memberRow
	if err := json.Unmarshal(memberJSON, &member); err != nil {
		return event.Event{}, fmt.Errorf("decoding created membership: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, $2, $3, 'create', NULL, $4::jsonb)`

	if _, err := tx.Exec(ctx, insertAudit, actorID, "event", row.ID, eventJSON); err != nil {
		return event.Event{}, fmt.Errorf("auditing created event: %w", err)
	}
	if _, err := tx.Exec(ctx, insertAudit, actorID, "event_member", member.ID, memberJSON); err != nil {
		return event.Event{}, fmt.Errorf("auditing created membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return event.Event{}, fmt.Errorf("committing create transaction: %w", err)
	}
	return row.toEvent(), nil
}

// Get returns the live (non-soft-deleted) event, or ErrNotFound.
func (r *EventRepository) Get(ctx context.Context, id string) (event.Event, error) {
	const q = `
		SELECT id, name, description, timezone, starts_at, ends_at, version, created_at, updated_at
		FROM events WHERE id = $1 AND deleted_at IS NULL`

	var e event.Event
	err := r.pool.QueryRow(ctx, q, id).Scan(&e.ID, &e.Name, &e.Description, &e.Timezone, &e.StartsAt, &e.EndsAt, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return event.Event{}, domain.ErrNotFound
		}
		return event.Event{}, fmt.Errorf("getting event: %w", err)
	}
	return e, nil
}

// List returns events userID has an event_members row for, ordered by
// (created_at, id) -- never OFFSET. It fetches one extra row to decide
// whether a further page exists, then trims it before returning.
func (r *EventRepository) List(ctx context.Context, userID string, limit int, after *event.Cursor) (event.Page, error) {
	const baseQuery = `
		SELECT e.id, e.name, e.description, e.timezone, e.starts_at, e.ends_at, e.version, e.created_at, e.updated_at
		FROM events e
		JOIN event_members em ON em.event_id = e.id
		WHERE em.user_id = $1 AND e.deleted_at IS NULL`

	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = r.pool.Query(ctx, baseQuery+` ORDER BY e.created_at, e.id LIMIT $2`, userID, limit+1)
	} else {
		rows, err = r.pool.Query(ctx,
			baseQuery+` AND (e.created_at, e.id) > ($2, $3) ORDER BY e.created_at, e.id LIMIT $4`,
			userID, after.CreatedAt, after.ID, limit+1)
	}
	if err != nil {
		return event.Page{}, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	var events []event.Event
	for rows.Next() {
		var e event.Event
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.Timezone, &e.StartsAt, &e.EndsAt, &e.Version, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return event.Page{}, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return event.Page{}, fmt.Errorf("listing events: %w", err)
	}

	page := event.Page{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[limit-1]
		page.NextCursor = &event.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// Update applies patch atomically, gated on version. The UPDATE and its
// audit row share one transaction; zero rows affected means the write
// never happened, decided by a diagnostic read after the fact, never a
// check beforehand.
func (r *EventRepository) Update(ctx context.Context, actorID, id string, version int, patch event.Patch) (event.Event, error) {
	record := goqu.Record{
		"updated_at": goqu.L("now()"),
		"version":    goqu.L("version + 1"),
	}
	if patch.Name.Set {
		record["name"] = patch.Name.Value
	}
	if patch.Description.Set {
		record["description"] = patch.Description.Value
	}
	if patch.Timezone.Set {
		record["timezone"] = patch.Timezone.Value
	}
	if patch.StartsAt.Set {
		record["starts_at"] = patch.StartsAt.Value
	}
	if patch.EndsAt.Set {
		record["ends_at"] = patch.EndsAt.Value
	}

	sql, args, err := dialect.Update("events").
		Set(record).
		Where(goqu.Ex{"id": id, "version": version, "deleted_at": nil}).
		Returning(
			goqu.L("to_jsonb(OLD)").As("before_row"),
			goqu.L("to_jsonb(NEW)").As("after_row"),
		).
		Prepared(true).
		ToSQL()
	if err != nil {
		return event.Event{}, fmt.Errorf("building update statement: %w", err)
	}

	return r.versionedWrite(ctx, actorID, id, "update", sql, args)
}

// Delete soft-deletes the event (sets deleted_at), gated on version the
// same way Update is.
func (r *EventRepository) Delete(ctx context.Context, actorID, id string, version int) (event.Event, error) {
	sql, args, err := dialect.Update("events").
		Set(goqu.Record{"deleted_at": goqu.L("now()"), "updated_at": goqu.L("now()"), "version": goqu.L("version + 1")}).
		Where(goqu.Ex{"id": id, "version": version, "deleted_at": nil}).
		Returning(
			goqu.L("to_jsonb(OLD)").As("before_row"),
			goqu.L("to_jsonb(NEW)").As("after_row"),
		).
		Prepared(true).
		ToSQL()
	if err != nil {
		return event.Event{}, fmt.Errorf("building delete statement: %w", err)
	}

	return r.versionedWrite(ctx, actorID, id, "delete", sql, args)
}

// versionedWrite runs a version-gated UPDATE that RETURNs to_jsonb(OLD)/
// to_jsonb(NEW), audits it in the same transaction, and disambiguates a
// zero-row result into ErrNotFound vs ErrVersionMismatch with a read that
// happens only after the write has already failed -- it decides nothing
// the write depended on.
func (r *EventRepository) versionedWrite(ctx context.Context, actorID, id, action, sql string, args []any) (event.Event, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return event.Event{}, fmt.Errorf("beginning %s transaction: %w", action, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var beforeJSON, afterJSON []byte
	err = tx.QueryRow(ctx, sql, args...).Scan(&beforeJSON, &afterJSON)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return event.Event{}, fmt.Errorf("%s event: %w", action, err)
		}

		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT true FROM events WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return event.Event{}, domain.ErrNotFound
			}
			return event.Event{}, fmt.Errorf("checking event existence after failed %s: %w", action, err)
		}
		return event.Event{}, domain.ErrVersionMismatch
	}

	var row eventRow
	if err := json.Unmarshal(afterJSON, &row); err != nil {
		return event.Event{}, fmt.Errorf("decoding %s result: %w", action, err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'event', $2, $3, $4::jsonb, $5::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, id, action, beforeJSON, afterJSON); err != nil {
		return event.Event{}, fmt.Errorf("auditing %s: %w", action, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return event.Event{}, fmt.Errorf("committing %s transaction: %w", action, err)
	}
	return row.toEvent(), nil
}
