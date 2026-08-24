package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
)

// foreignKeyViolation is Postgres's SQLSTATE for a foreign-key violation --
// sessions.room_id has no ON DELETE action, so once item 12 ships, deleting
// a room that still has sessions referencing it fires this.
const foreignKeyViolation = "23503"

// exclusionViolation is Postgres's SQLSTATE for an EXCLUDE constraint
// violation -- item 16's sessions_room_no_overlap_excl /
// sessions_speaker_no_overlap_excl fire this.
const exclusionViolation = "23P01"

// RoomRepository implements domain/room.Repository.
type RoomRepository struct {
	pool *pgxpool.Pool
}

func NewRoomRepository(pool *pgxpool.Pool) *RoomRepository {
	return &RoomRepository{pool: pool}
}

// roomRow mirrors the rooms table's JSON shape -- to_jsonb(rooms) keys
// match column names exactly, matching event_repo.go's eventRow precedent.
type roomRow struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	Name      string    `json:"name"`
	Capacity  *int      `json:"capacity"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r roomRow) toRoom() room.Room {
	return room.Room{
		ID:        r.ID,
		EventID:   r.EventID,
		Name:      r.Name,
		Capacity:  r.Capacity,
		Version:   r.Version,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// Create inserts the room and audits it in the same transaction --
// row_to_json(new_room) via a CTE, the same shape event_repo.go's Create
// and member_repo.go's Create both use for a single-row insert's audit
// "after". A unique violation on rooms_event_id_name_key maps to
// domain.ErrConflict.
func (r *RoomRepository) Create(ctx context.Context, actorID, eventID string, in room.CreateInput) (room.Room, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return room.Room{}, fmt.Errorf("beginning create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		WITH new_room AS (
			INSERT INTO rooms (event_id, name, capacity)
			VALUES ($1, $2, $3)
			RETURNING id, event_id, name, capacity, version, created_at, updated_at
		)
		SELECT row_to_json(new_room) FROM new_room`

	var roomJSON []byte
	err = tx.QueryRow(ctx, insert, eventID, in.Name, in.Capacity).Scan(&roomJSON)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return room.Room{}, domain.ErrConflict
		}
		return room.Room{}, fmt.Errorf("creating room: %w", err)
	}

	var row roomRow
	if err := json.Unmarshal(roomJSON, &row); err != nil {
		return room.Room{}, fmt.Errorf("decoding created room: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'room', $2, 'create', NULL, $3::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, row.ID, roomJSON); err != nil {
		return room.Room{}, fmt.Errorf("auditing created room: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return room.Room{}, fmt.Errorf("committing create transaction: %w", err)
	}
	return row.toRoom(), nil
}

// Get returns the room, scoped to both id and eventID -- a room belonging
// to a different event is indistinguishable from a missing one.
func (r *RoomRepository) Get(ctx context.Context, eventID, roomID string) (room.Room, error) {
	const q = `
		SELECT id, event_id, name, capacity, version, created_at, updated_at
		FROM rooms WHERE id = $1 AND event_id = $2`

	var rm room.Room
	err := r.pool.QueryRow(ctx, q, roomID, eventID).
		Scan(&rm.ID, &rm.EventID, &rm.Name, &rm.Capacity, &rm.Version, &rm.CreatedAt, &rm.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return room.Room{}, domain.ErrNotFound
		}
		return room.Room{}, fmt.Errorf("getting room: %w", err)
	}
	return rm, nil
}

// List returns eventID's rooms, ordered by (created_at, id) -- never
// OFFSET. It fetches one extra row to decide whether a further page
// exists, then trims it before returning, matching event_repo.go's List.
func (r *RoomRepository) List(ctx context.Context, eventID string, limit int, after *room.Cursor) (room.Page, error) {
	const baseQuery = `
		SELECT id, event_id, name, capacity, version, created_at, updated_at
		FROM rooms WHERE event_id = $1`

	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = r.pool.Query(ctx, baseQuery+` ORDER BY created_at, id LIMIT $2`, eventID, limit+1)
	} else {
		rows, err = r.pool.Query(ctx,
			baseQuery+` AND (created_at, id) > ($2, $3) ORDER BY created_at, id LIMIT $4`,
			eventID, after.CreatedAt, after.ID, limit+1)
	}
	if err != nil {
		return room.Page{}, fmt.Errorf("listing rooms: %w", err)
	}
	defer rows.Close()

	var rooms []room.Room
	for rows.Next() {
		var rm room.Room
		if err := rows.Scan(&rm.ID, &rm.EventID, &rm.Name, &rm.Capacity, &rm.Version, &rm.CreatedAt, &rm.UpdatedAt); err != nil {
			return room.Page{}, fmt.Errorf("scanning room: %w", err)
		}
		rooms = append(rooms, rm)
	}
	if err := rows.Err(); err != nil {
		return room.Page{}, fmt.Errorf("listing rooms: %w", err)
	}

	page := room.Page{Rooms: rooms}
	if len(rooms) > limit {
		page.Rooms = rooms[:limit]
		last := page.Rooms[limit-1]
		page.NextCursor = &room.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// Update applies patch atomically, gated on version and scoped to eventID.
// RETURNING to_jsonb(OLD)/to_jsonb(NEW) is the exact audit-capture form
// member_repo.go's AssignRole/Delete already use (verified against real
// Postgres 18 during planning), reused here as-is. A unique violation on
// rename maps to domain.ErrConflict.
func (r *RoomRepository) Update(ctx context.Context, actorID, eventID, roomID string, version int, patch room.Patch) (room.Room, error) {
	record := goqu.Record{
		"updated_at": goqu.L("now()"),
		"version":    goqu.L("version + 1"),
	}
	if patch.Name.Set {
		record["name"] = patch.Name.Value
	}
	if patch.Capacity.Set {
		record["capacity"] = patch.Capacity.Value
	}

	sql, args, err := dialect.Update("rooms").
		Set(record).
		Where(goqu.Ex{"id": roomID, "event_id": eventID, "version": version}).
		Returning(
			goqu.L("to_jsonb(OLD)").As("before_row"),
			goqu.L("to_jsonb(NEW)").As("after_row"),
		).
		Prepared(true).
		ToSQL()
	if err != nil {
		return room.Room{}, fmt.Errorf("building update statement: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return room.Room{}, fmt.Errorf("beginning update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var beforeJSON, afterJSON []byte
	err = tx.QueryRow(ctx, sql, args...).Scan(&beforeJSON, &afterJSON)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return room.Room{}, domain.ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return room.Room{}, fmt.Errorf("updating room: %w", err)
		}

		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT true FROM rooms WHERE id = $1 AND event_id = $2`, roomID, eventID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return room.Room{}, domain.ErrNotFound
			}
			return room.Room{}, fmt.Errorf("checking room existence after failed update: %w", err)
		}
		return room.Room{}, domain.ErrVersionMismatch
	}

	var row roomRow
	if err := json.Unmarshal(afterJSON, &row); err != nil {
		return room.Room{}, fmt.Errorf("decoding update result: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'room', $2, 'update', $3::jsonb, $4::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, roomID, beforeJSON, afterJSON); err != nil {
		return room.Room{}, fmt.Errorf("auditing update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return room.Room{}, fmt.Errorf("committing update transaction: %w", err)
	}
	return row.toRoom(), nil
}

// Delete hard-deletes the room, scoped to eventID and gated on version --
// rooms has no deleted_at column, unlike events/sessions. RETURNING
// to_jsonb(OLD) on the DELETE itself is the same form member_repo.go's
// Delete uses. A foreign-key violation (sessions still referencing this
// room) maps to domain.ErrConflict -- the caller must remove or reassign
// those sessions first; there is no cascade.
func (r *RoomRepository) Delete(ctx context.Context, actorID, eventID, roomID string, version int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const del = `
		DELETE FROM rooms
		WHERE id = $1 AND event_id = $2 AND version = $3
		RETURNING to_jsonb(OLD) AS before_row`

	var beforeJSON []byte
	err = tx.QueryRow(ctx, del, roomID, eventID, version).Scan(&beforeJSON)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return domain.ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("deleting room: %w", err)
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM rooms WHERE id = $1 AND event_id = $2`, roomID, eventID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("checking room existence after failed delete: %w", err)
		}
		return domain.ErrVersionMismatch
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'room', $2, 'delete', $3::jsonb, NULL)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, roomID, beforeJSON); err != nil {
		return fmt.Errorf("auditing delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing delete transaction: %w", err)
	}
	return nil
}
