package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
)

// uniqueViolation is Postgres's SQLSTATE for a unique-constraint violation --
// event_members_event_id_user_id_key firing on a duplicate add.
const uniqueViolation = "23505"

// AfterEventLockForTest is a test-only synchronization seam -- NOT FOR
// PRODUCTION USE, same pattern as middleware.ContextWithUser. Only
// member_repo_race_test.go ever sets it.
//
// It exists because a bare goroutine-barrier race test cannot reliably
// prove AssignRole/Delete's SELECT ... FOR UPDATE actually serializes
// admin-count-affecting writes: the guarded write executes fast enough that
// two goroutines almost never land inside the genuinely vulnerable window
// on their own. Verified during development -- a barrier-only race test
// with no seam passed 20/20 runs even with the FOR UPDATE lock deliberately
// removed, and only started failing once the vulnerable window was forced
// open (see ISSUE.md). This hook lets the shipped race test force that same
// overlap deterministically, every run, without ever touching the SQL
// itself: it is called immediately after the events-row lock is acquired,
// so if two goroutines are ever both "inside" it at once, the lock did not
// serialize them.
//
// It is called only through afterEventLock below, which gates every call on
// testing.Testing() -- this seam sits inside a transaction that is holding
// a real row lock, so a misfire in a production binary (a stray import, a
// copy-pasted assignment) would pause real requests, not just a test. Since
// testing.Testing() is hardwired false outside a go test binary, the guard
// holds even if AfterEventLockForTest is somehow set to something
// non-trivial in code that ends up in a production build.
var AfterEventLockForTest = func() {}

// afterEventLock is the only call site AssignRole/Delete use. See
// AfterEventLockForTest's doc comment for why the testing.Testing() guard
// is load-bearing, not decorative.
func afterEventLock() {
	if testing.Testing() {
		AfterEventLockForTest()
	}
}

// MemberRepository implements domain/member.Repository.
type MemberRepository struct {
	pool *pgxpool.Pool
}

func NewMemberRepository(pool *pgxpool.Pool) *MemberRepository {
	return &MemberRepository{pool: pool}
}

// eventMemberRow mirrors event_members' JSON shape plus the users join --
// to_jsonb(event_members)'s keys match column names exactly, so this is
// what audit_log's before/after and the RETURNING payloads decode into.
type eventMemberRow struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r eventMemberRow) toMember(email, name string) member.Member {
	return member.Member{
		ID:        r.ID,
		EventID:   r.EventID,
		UserID:    r.UserID,
		UserEmail: email,
		UserName:  name,
		Role:      r.Role,
		Version:   r.Version,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// Create resolves in.Email to an existing user and grants them in.Role on
// eventID, in one atomic statement: the same INSERT...SELECT both resolves
// the email and enforces that actorID may grant that role (only an admin
// may grant anything but attendee -- see roles.go's canGrantRoleGuard,
// shared verbatim with InvitationRepository.Create, item 13), so there is
// no separate privilege check that could race the insert. Verified against
// real Postgres 18.6 before this was written -- see the feature-09 plan's
// Gate-0 proof.
func (r *MemberRepository) Create(ctx context.Context, actorID, eventID string, in member.CreateInput) (member.Member, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return member.Member{}, fmt.Errorf("beginning create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		WITH actor AS (
			` + canGrantRoleActorCTE + `
		),
		new_member AS (
			INSERT INTO event_members (event_id, user_id, role)
			SELECT $1, u.id, $3
			FROM users u
			WHERE u.email = $2
			  AND ` + canGrantRoleGuard + `
			RETURNING id, event_id, user_id, role, version, created_at, updated_at
		)
		SELECT
			(SELECT row_to_json(new_member) FROM new_member),
			(SELECT u.email FROM new_member nm JOIN users u ON u.id = nm.user_id)`

	var memberJSON []byte
	var email *string
	err = tx.QueryRow(ctx, insert, eventID, in.Email, in.Role, actorID).Scan(&memberJSON, &email)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return member.Member{}, domain.ErrConflict
		}
		return member.Member{}, fmt.Errorf("creating member: %w", err)
	}

	if memberJSON == nil {
		// Zero rows: either the email resolved to no user, or the actor
		// isn't allowed to grant that role. A diagnostic read after the
		// failed write -- never before it -- disambiguates which.
		var userExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, in.Email).Scan(&userExists); err != nil {
			return member.Member{}, fmt.Errorf("checking user existence after failed create: %w", err)
		}
		if !userExists {
			return member.Member{}, domain.ErrNotFound
		}
		return member.Member{}, domain.ErrForbidden
	}

	var row eventMemberRow
	if err := json.Unmarshal(memberJSON, &row); err != nil {
		return member.Member{}, fmt.Errorf("decoding created member: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'event_member', $2, 'create', NULL, $3::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, row.ID, memberJSON); err != nil {
		return member.Member{}, fmt.Errorf("auditing created member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return member.Member{}, fmt.Errorf("committing create transaction: %w", err)
	}

	emailStr := ""
	if email != nil {
		emailStr = *email
	}
	return row.toMember(emailStr, ""), nil
}

// List returns eventID's roster, ordered by (created_at, id) -- never
// OFFSET. It fetches one extra row to decide whether a further page
// exists, then trims it before returning.
func (r *MemberRepository) List(ctx context.Context, eventID string, limit int, after *member.Cursor) (member.Page, error) {
	const baseQuery = `
		SELECT em.id, em.event_id, em.user_id, em.role, em.version, em.created_at, em.updated_at, u.email, u.name
		FROM event_members em
		JOIN users u ON u.id = em.user_id
		WHERE em.event_id = $1`

	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = r.pool.Query(ctx, baseQuery+` ORDER BY em.created_at, em.id LIMIT $2`, eventID, limit+1)
	} else {
		rows, err = r.pool.Query(ctx,
			baseQuery+` AND (em.created_at, em.id) > ($2, $3) ORDER BY em.created_at, em.id LIMIT $4`,
			eventID, after.CreatedAt, after.ID, limit+1)
	}
	if err != nil {
		return member.Page{}, fmt.Errorf("listing members: %w", err)
	}
	defer rows.Close()

	var members []member.Member
	for rows.Next() {
		var m member.Member
		if err := rows.Scan(&m.ID, &m.EventID, &m.UserID, &m.Role, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.UserEmail, &m.UserName); err != nil {
			return member.Page{}, fmt.Errorf("scanning member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return member.Page{}, fmt.Errorf("listing members: %w", err)
	}

	page := member.Page{Members: members}
	if len(members) > limit {
		page.Members = members[:limit]
		last := page.Members[limit-1]
		page.NextCursor = &member.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// AssignRole changes memberID's role, gated on version. Any mutation that
// could change eventID's admin count first takes SELECT ... FOR UPDATE on
// the events row, serializing every such mutation on that event so the
// admin-count guard below is checked against a fresh, consistent count --
// without the lock, two concurrent demotions of the last two admins could
// each see the other still present and both succeed, orphaning the event.
// EVERY write path that could change eventID's admin count MUST take this
// same lock first (see FAILURES.md) -- nothing but this comment and that
// convention enforces it.
func (r *MemberRepository) AssignRole(ctx context.Context, actorID, eventID, memberID string, version int, role string) (member.Member, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return member.Member{}, fmt.Errorf("beginning assign-role transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT id FROM events WHERE id = $1 FOR UPDATE`, eventID); err != nil {
		return member.Member{}, fmt.Errorf("locking event for assign-role: %w", err)
	}
	afterEventLock()

	const update = `
		UPDATE event_members
		SET role = $1, updated_at = now(), version = version + 1
		WHERE id = $2 AND event_id = $3 AND version = $4
		  AND (
		    role <> 'admin'
		    OR $1 = 'admin'
		    OR EXISTS (SELECT 1 FROM event_members other WHERE other.event_id = $3 AND other.role = 'admin' AND other.id <> $2)
		  )
		RETURNING to_jsonb(OLD) AS before_row, to_jsonb(NEW) AS after_row`

	var beforeJSON, afterJSON []byte
	err = tx.QueryRow(ctx, update, role, memberID, eventID, version).Scan(&beforeJSON, &afterJSON)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return member.Member{}, fmt.Errorf("assigning role: %w", err)
		}
		return member.Member{}, r.diagnoseAdminGuardedFailure(ctx, tx, eventID, memberID, version)
	}

	var row eventMemberRow
	if err := json.Unmarshal(afterJSON, &row); err != nil {
		return member.Member{}, fmt.Errorf("decoding assign-role result: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'event_member', $2, 'update', $3::jsonb, $4::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, memberID, beforeJSON, afterJSON); err != nil {
		return member.Member{}, fmt.Errorf("auditing assign-role: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return member.Member{}, fmt.Errorf("committing assign-role transaction: %w", err)
	}
	return row.toMember("", ""), nil
}

// Delete removes memberID from eventID, gated on version and the same
// last-admin guard as AssignRole -- see its comment for the locking
// protocol every admin-count-affecting write path must follow. A real hard
// DELETE: event_members has no deleted_at column.
func (r *MemberRepository) Delete(ctx context.Context, actorID, eventID, memberID string, version int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT id FROM events WHERE id = $1 FOR UPDATE`, eventID); err != nil {
		return fmt.Errorf("locking event for member delete: %w", err)
	}
	afterEventLock()

	const del = `
		DELETE FROM event_members
		WHERE id = $1 AND event_id = $2 AND version = $3
		  AND (
		    role <> 'admin'
		    OR EXISTS (SELECT 1 FROM event_members other WHERE other.event_id = $2 AND other.role = 'admin' AND other.id <> $1)
		  )
		RETURNING to_jsonb(OLD) AS before_row`

	var beforeJSON []byte
	err = tx.QueryRow(ctx, del, memberID, eventID, version).Scan(&beforeJSON)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("deleting member: %w", err)
		}
		return r.diagnoseAdminGuardedFailure(ctx, tx, eventID, memberID, version)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'event_member', $2, 'delete', $3::jsonb, NULL)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, memberID, beforeJSON); err != nil {
		return fmt.Errorf("auditing member delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing delete transaction: %w", err)
	}
	return nil
}

// diagnoseAdminGuardedFailure runs after AssignRole/Delete's write affected
// zero rows, inside the same transaction (still holding the events row
// lock), to decide why: the row doesn't exist (ErrNotFound), its version no
// longer matches (ErrVersionMismatch), or it's eventID's last admin and the
// write would have zeroed the admin count (ErrConflict). This is a
// diagnostic read after a failed write, not a check before one -- the write
// itself already decided atomically; this only classifies the failure.
func (r *MemberRepository) diagnoseAdminGuardedFailure(ctx context.Context, tx pgx.Tx, eventID, memberID string, version int) error {
	var currentVersion int
	var role string
	err := tx.QueryRow(ctx, `SELECT version, role FROM event_members WHERE id = $1 AND event_id = $2`, memberID, eventID).
		Scan(&currentVersion, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("diagnosing failed write: %w", err)
	}
	if currentVersion != version {
		return domain.ErrVersionMismatch
	}
	// Version matches and the row exists, so the write's own guard must
	// have blocked it: this is eventID's last admin.
	return domain.ErrConflict
}
