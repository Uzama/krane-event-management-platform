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
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

// SessionRepository implements domain/session.Repository.
type SessionRepository struct {
	pool *pgxpool.Pool
}

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// sessionRow mirrors Create's row_to_json shape -- lower(time_range)/
// upper(time_range) are aliased to starts_at/ends_at in the SQL itself, so
// this never has to parse a tstzrange's own JSON string form.
type sessionRow struct {
	ID          string    `json:"id"`
	EventID     string    `json:"event_id"`
	RoomID      string    `json:"room_id"`
	SpeakerID   string    `json:"speaker_id"`
	Title       string    `json:"title"`
	Description *string   `json:"description"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (r sessionRow) toSession() session.Session {
	return session.Session{
		ID:          r.ID,
		EventID:     r.EventID,
		RoomID:      r.RoomID,
		SpeakerID:   r.SpeakerID,
		Title:       r.Title,
		Description: r.Description,
		StartsAt:    r.StartsAt,
		EndsAt:      r.EndsAt,
		Version:     r.Version,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// Create inserts the session and audits it in the same transaction. RoomID
// is resolved atomically: the INSERT's source rows come from a SELECT
// against rooms scoped to both id and eventID, so a room belonging to a
// different event -- or not existing at all -- is a single atomic decision,
// never a check-then-act SELECT beforehand (mirrors member_repo.go's
// email-lookup Create). The outer SELECT wraps row_to_json in a scalar
// subquery, the same trick member_repo.go's Create uses, so the query
// always returns exactly one row (with a NULL value when the room lookup
// matched nothing) instead of pgx.ErrNoRows -- that NULL is how a bad room
// reference is told apart from a genuine query failure.
//
// SpeakerID has a real foreign key (sessions_speaker_id_fkey) that fires
// independently as a 23503 violation, since it isn't part of the room
// lookup's WHERE clause. Precedence is deterministic: when the room lookup
// matches nothing, the INSERT's SELECT produces zero source rows, so the
// INSERT never executes and the speaker foreign key is never evaluated at
// all -- a request with both an invalid room and an invalid speaker always
// fails as ErrInvalidRoom, never ErrInvalidSpeaker.
func (r *SessionRepository) Create(ctx context.Context, actorID, eventID string, in session.CreateInput) (session.Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return session.Session{}, fmt.Errorf("beginning create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		WITH new_session AS (
			INSERT INTO sessions (event_id, room_id, speaker_id, title, description, time_range)
			SELECT $1, rm.id, $3, $4, $5, tstzrange($6, $7, '[)')
			FROM rooms rm
			WHERE rm.id = $2 AND rm.event_id = $1
			RETURNING id, event_id, room_id, speaker_id, title, description,
			          lower(time_range) AS starts_at, upper(time_range) AS ends_at,
			          version, created_at, updated_at
		)
		SELECT (SELECT row_to_json(new_session) FROM new_session)`

	var sessionJSON []byte
	err = tx.QueryRow(ctx, insert, eventID, in.RoomID, in.SpeakerID, in.Title, in.Description, in.StartsAt, in.EndsAt).
		Scan(&sessionJSON)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == "sessions_speaker_id_fkey" {
			return session.Session{}, session.ErrInvalidSpeaker
		}
		if errors.As(err, &pgErr) && pgErr.Code == exclusionViolation {
			// Either sessions_room_no_overlap_excl or
			// sessions_speaker_no_overlap_excl (item 16) -- both mean the
			// same thing to the caller: this slot conflicts with an
			// existing booking. domain.ErrConflict is the one vocabulary
			// http maps to 409, same as room_repo.go's name collision.
			return session.Session{}, domain.ErrConflict
		}
		return session.Session{}, fmt.Errorf("creating session: %w", err)
	}

	if sessionJSON == nil {
		return session.Session{}, session.ErrInvalidRoom
	}

	var row sessionRow
	if err := json.Unmarshal(sessionJSON, &row); err != nil {
		return session.Session{}, fmt.Errorf("decoding created session: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'session', $2, 'create', NULL, $3::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, row.ID, sessionJSON); err != nil {
		return session.Session{}, fmt.Errorf("auditing created session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return session.Session{}, fmt.Errorf("committing create transaction: %w", err)
	}
	return row.toSession(), nil
}

// Get returns the live (non-soft-deleted) session, scoped to both id and
// eventID -- a session belonging to a different event is indistinguishable
// from a missing one, same as room.Repository.Get.
func (r *SessionRepository) Get(ctx context.Context, eventID, sessionID string) (session.Session, error) {
	const q = `
		SELECT id, event_id, room_id, speaker_id, title, description,
		       lower(time_range), upper(time_range), version, created_at, updated_at
		FROM sessions WHERE id = $1 AND event_id = $2 AND deleted_at IS NULL`

	var s session.Session
	err := r.pool.QueryRow(ctx, q, sessionID, eventID).Scan(
		&s.ID, &s.EventID, &s.RoomID, &s.SpeakerID, &s.Title, &s.Description,
		&s.StartsAt, &s.EndsAt, &s.Version, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return session.Session{}, domain.ErrNotFound
		}
		return session.Session{}, fmt.Errorf("getting session: %w", err)
	}
	return s, nil
}

// List returns eventID's live sessions, ordered by (created_at, id) --
// never OFFSET. It fetches one extra row to decide whether a further page
// exists, then trims it before returning, matching room_repo.go's List.
//
// Item 18: room_name/speaker_name are batched via a single JOIN against
// rooms/users in this same query, not a per-row lookup -- the query count
// for this call is exactly 1 whether the page holds 1 session or the
// limit's maximum, matching member_repo.go's List precedent (it already
// joins users for email/name the same way).
func (r *SessionRepository) List(ctx context.Context, eventID string, limit int, after *session.Cursor) (session.Page, error) {
	const baseQuery = `
		SELECT s.id, s.event_id, s.room_id, s.speaker_id, s.title, s.description,
		       lower(s.time_range), upper(s.time_range), s.version, s.created_at, s.updated_at,
		       rm.name, spk.name
		FROM sessions s
		JOIN rooms rm ON rm.id = s.room_id
		JOIN users spk ON spk.id = s.speaker_id
		WHERE s.event_id = $1 AND s.deleted_at IS NULL`

	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = r.pool.Query(ctx, baseQuery+` ORDER BY s.created_at, s.id LIMIT $2`, eventID, limit+1)
	} else {
		rows, err = r.pool.Query(ctx,
			baseQuery+` AND (s.created_at, s.id) > ($2, $3) ORDER BY s.created_at, s.id LIMIT $4`,
			eventID, after.CreatedAt, after.ID, limit+1)
	}
	if err != nil {
		return session.Page{}, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []session.Session
	for rows.Next() {
		var s session.Session
		if err := rows.Scan(&s.ID, &s.EventID, &s.RoomID, &s.SpeakerID, &s.Title, &s.Description,
			&s.StartsAt, &s.EndsAt, &s.Version, &s.CreatedAt, &s.UpdatedAt,
			&s.RoomName, &s.SpeakerName); err != nil {
			return session.Page{}, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return session.Page{}, fmt.Errorf("listing sessions: %w", err)
	}

	page := session.Page{Sessions: sessions}
	if len(sessions) > limit {
		page.Sessions = sessions[:limit]
		last := page.Sessions[limit-1]
		page.NextCursor = &session.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// sessionReturningColumns is shared by Update and Delete (a soft delete is
// itself an UPDATE): explicit scalar columns for the returned
// domain.Session -- lower(time_range)/upper(time_range) aliased so no
// tstzrange JSON parsing is ever needed -- plus to_jsonb(OLD)/to_jsonb(NEW)
// for the audit row, the same literal RETURNING form verified live against
// room_repo.go/member_repo.go before this was written.
func sessionReturningColumns() []interface{} {
	return []interface{}{
		goqu.C("id"), goqu.C("event_id"), goqu.C("room_id"), goqu.C("speaker_id"),
		goqu.C("title"), goqu.C("description"),
		goqu.L("lower(time_range)").As("starts_at"), goqu.L("upper(time_range)").As("ends_at"),
		goqu.C("version"), goqu.C("created_at"), goqu.C("updated_at"),
		goqu.L("to_jsonb(OLD)").As("before_row"), goqu.L("to_jsonb(NEW)").As("after_row"),
	}
}

// Update applies patch atomically, gated on version and scoped to eventID,
// excluding soft-deleted rows. RoomID/SpeakerID are never in patch --
// they're fixed at creation (docs/requirements.md §8). StartsAt/EndsAt
// arrive here both-or-neither (http/request enforces the pair, since
// time_range is one column and tstzrange(?, ?) needs both bounds to build
// at all -- unlike event.Patch's two independent scalar columns).
func (r *SessionRepository) Update(ctx context.Context, actorID, eventID, sessionID string, version int, patch session.Patch) (session.Session, error) {
	record := goqu.Record{
		"updated_at": goqu.L("now()"),
		"version":    goqu.L("version + 1"),
	}
	if patch.Title.Set {
		record["title"] = patch.Title.Value
	}
	if patch.Description.Set {
		record["description"] = patch.Description.Value
	}
	if patch.StartsAt.Set && patch.EndsAt.Set {
		record["time_range"] = goqu.L("tstzrange(?, ?, '[)')", patch.StartsAt.Value, patch.EndsAt.Value)
	}

	sql, args, err := dialect.Update("sessions").
		Set(record).
		Where(goqu.Ex{"id": sessionID, "event_id": eventID, "version": version, "deleted_at": nil}).
		Returning(sessionReturningColumns()...).
		Prepared(true).
		ToSQL()
	if err != nil {
		return session.Session{}, fmt.Errorf("building update statement: %w", err)
	}

	return r.versionedWrite(ctx, actorID, eventID, sessionID, "update", sql, args)
}

// Delete soft-deletes the session (sets deleted_at), gated on version the
// same way Update is -- matching events, not rooms: docs/requirements.md
// D9 says cancelling a session frees its room and speaker slot immediately,
// which only makes sense against a soft delete plus item 16's later
// partial EXCLUDE constraints.
func (r *SessionRepository) Delete(ctx context.Context, actorID, eventID, sessionID string, version int) (session.Session, error) {
	sql, args, err := dialect.Update("sessions").
		Set(goqu.Record{
			"deleted_at": goqu.L("now()"),
			"updated_at": goqu.L("now()"),
			"version":    goqu.L("version + 1"),
		}).
		Where(goqu.Ex{"id": sessionID, "event_id": eventID, "version": version, "deleted_at": nil}).
		Returning(sessionReturningColumns()...).
		Prepared(true).
		ToSQL()
	if err != nil {
		return session.Session{}, fmt.Errorf("building delete statement: %w", err)
	}

	return r.versionedWrite(ctx, actorID, eventID, sessionID, "delete", sql, args)
}

// versionedWrite runs a version-gated UPDATE that RETURNs sessionReturningColumns'
// scalar fields plus to_jsonb(OLD)/to_jsonb(NEW), audits it in the same
// transaction, and disambiguates a zero-row result into ErrNotFound
// (missing, wrong event, or already soft-deleted) vs ErrVersionMismatch
// with a read that happens only after the write has already failed -- it
// decides nothing the write depended on. Shared by Update and Delete,
// matching event_repo.go's precedent.
func (r *SessionRepository) versionedWrite(ctx context.Context, actorID, eventID, sessionID, action, sql string, args []any) (session.Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return session.Session{}, fmt.Errorf("beginning %s transaction: %w", action, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var s session.Session
	var beforeJSON, afterJSON []byte
	err = tx.QueryRow(ctx, sql, args...).Scan(
		&s.ID, &s.EventID, &s.RoomID, &s.SpeakerID, &s.Title, &s.Description,
		&s.StartsAt, &s.EndsAt, &s.Version, &s.CreatedAt, &s.UpdatedAt,
		&beforeJSON, &afterJSON,
	)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return session.Session{}, fmt.Errorf("%s session: %w", action, err)
		}

		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT true FROM sessions WHERE id = $1 AND event_id = $2 AND deleted_at IS NULL`,
			sessionID, eventID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return session.Session{}, domain.ErrNotFound
			}
			return session.Session{}, fmt.Errorf("checking session existence after failed %s: %w", action, err)
		}
		return session.Session{}, domain.ErrVersionMismatch
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'session', $2, $3, $4::jsonb, $5::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, sessionID, action, beforeJSON, afterJSON); err != nil {
		return session.Session{}, fmt.Errorf("auditing %s: %w", action, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return session.Session{}, fmt.Errorf("committing %s transaction: %w", action, err)
	}
	return s, nil
}
