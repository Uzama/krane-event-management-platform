package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/domain"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
)

// InvitationRepository implements domain/invitation.Repository.
type InvitationRepository struct {
	pool *pgxpool.Pool
}

func NewInvitationRepository(pool *pgxpool.Pool) *InvitationRepository {
	return &InvitationRepository{pool: pool}
}

// invitationRow mirrors invitations' JSON shape -- row_to_json(new_invitation)
// keys match column names exactly, matching member_repo.go's eventMemberRow
// precedent.
type invitationRow struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    *string   `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r invitationRow) toInvitation() invitation.Invitation {
	return invitation.Invitation{
		ID:        r.ID,
		EventID:   r.EventID,
		UserID:    r.UserID,
		Email:     r.Email,
		Role:      r.Role,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// Create invites in.Email to eventID at in.Role in one atomic statement:
// the same INSERT...SELECT both resolves in.Email to an existing user (if
// any -- unmatched is not an error, D2, unlike member add's required
// match) and enforces that actorID may invite at that role. The guard is
// roles.go's canGrantRoleActorCTE/canGrantRoleGuard, the exact fragments
// MemberRepository.Create uses (item 09) -- only an admin may invite
// anything but attendee, and it fails closed for a non-member actor. Since
// the guard is the query's only conditional, zero rows always means the
// guard blocked it (ErrForbidden); a real duplicate-email conflict raises
// a unique-violation error instead of filtering rows, so the two failure
// modes can never be confused.
func (r *InvitationRepository) Create(ctx context.Context, actorID, eventID string, in invitation.CreateInput) (invitation.Invitation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return invitation.Invitation{}, fmt.Errorf("beginning create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		WITH actor AS (
			` + canGrantRoleActorCTE + `
		),
		new_invitation AS (
			INSERT INTO invitations (event_id, user_id, email, role)
			SELECT $1, (SELECT id FROM users WHERE email = $2), $2, $3
			WHERE ` + canGrantRoleGuard + `
			RETURNING id, event_id, user_id, email, role, created_at, updated_at
		)
		SELECT (SELECT row_to_json(new_invitation) FROM new_invitation)`

	var invitationJSON []byte
	err = tx.QueryRow(ctx, insert, eventID, in.Email, in.Role, actorID).Scan(&invitationJSON)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return invitation.Invitation{}, domain.ErrConflict
		}
		return invitation.Invitation{}, fmt.Errorf("creating invitation: %w", err)
	}
	if invitationJSON == nil {
		// The guard's the only conditional in the query, so zero rows can
		// only mean it blocked the write -- actorID has no membership on
		// eventID at all, or is a contributor inviting above attendee.
		return invitation.Invitation{}, domain.ErrForbidden
	}

	var row invitationRow
	if err := json.Unmarshal(invitationJSON, &row); err != nil {
		return invitation.Invitation{}, fmt.Errorf("decoding created invitation: %w", err)
	}

	const insertAudit = `
		INSERT INTO audit_log (actor_id, entity_type, entity_id, action, before, after)
		VALUES ($1, 'invitation', $2, 'create', NULL, $3::jsonb)`
	if _, err := tx.Exec(ctx, insertAudit, actorID, row.ID, invitationJSON); err != nil {
		return invitation.Invitation{}, fmt.Errorf("auditing created invitation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return invitation.Invitation{}, fmt.Errorf("committing create transaction: %w", err)
	}
	return row.toInvitation(), nil
}

// List returns eventID's invitations, ordered by (created_at, id) -- never
// OFFSET. It fetches one extra row to decide whether a further page
// exists, then trims it before returning, matching room_repo.go's List.
func (r *InvitationRepository) List(ctx context.Context, eventID string, limit int, after *invitation.Cursor) (invitation.Page, error) {
	const baseQuery = `
		SELECT id, event_id, user_id, email, role, created_at, updated_at
		FROM invitations WHERE event_id = $1`

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
		return invitation.Page{}, fmt.Errorf("listing invitations: %w", err)
	}
	defer rows.Close()

	var invitations []invitation.Invitation
	for rows.Next() {
		var inv invitation.Invitation
		if err := rows.Scan(&inv.ID, &inv.EventID, &inv.UserID, &inv.Email, &inv.Role, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
			return invitation.Page{}, fmt.Errorf("scanning invitation: %w", err)
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return invitation.Page{}, fmt.Errorf("listing invitations: %w", err)
	}

	page := invitation.Page{Invitations: invitations}
	if len(invitations) > limit {
		page.Invitations = invitations[:limit]
		last := page.Invitations[limit-1]
		page.NextCursor = &invitation.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// bulkInviteEndpoint is the fixed idempotency_keys.endpoint value for
// BulkCreate -- there is exactly one caller of this method, so this never
// needs to be a parameter.
const bulkInviteEndpoint = "POST /v1/events/{eventId}/invitations/bulk"

// BulkCreate implements item 21. See invitation.Repository.BulkCreate's
// doc comment for the full contract; this is the mechanics.
//
// A first call for (actorID, idempotencyKey) processes items one by one,
// EACH through Create -- the exact same atomic, escalation-guarded,
// audited single-invite write item 13 shipped, called in a loop, never a
// raw multi-row INSERT that would bypass the per-item guard (item 13's own
// forward-note to this item). Every successful item therefore gets its own
// audit_log row, written inside Create's own transaction, exactly like a
// single invite -- there is no batch-level audit row.
//
// The result is recorded with INSERT ... ON CONFLICT (actor_id, key) DO
// NOTHING RETURNING id: if this call wins the race, its own freshly
// computed result is authoritative. If it loses (a concurrent call for the
// same key committed first), the winner's stored result is read back and
// returned instead -- never this call's own local results, which may
// differ per-item from the winner's since both processed the same items
// concurrently and Postgres's own invitations_event_id_email_key unique
// constraint decides, per email, which caller's Create actually won that
// row. This still satisfies "never double-send": the constraint means at
// most one invitation row ever exists per (event, email) regardless of
// which caller's local view ends up unreported.
//
// A retry (the key already has a row before this call ever begins
// processing) either replays that row verbatim -- decoded from
// response_body, never recomputed against current state -- if
// requestHash matches, or returns domain.ErrConflict if it doesn't (the
// key was reused for a different request).
func (r *InvitationRepository) BulkCreate(ctx context.Context, actorID, eventID, idempotencyKey, requestHash string, items []invitation.CreateInput) (invitation.BulkResult, error) {
	if existing, found, err := r.loadIdempotencyResult(ctx, actorID, idempotencyKey, requestHash); err != nil {
		return invitation.BulkResult{}, err
	} else if found {
		existing.Replayed = true
		return existing, nil
	}

	results := make([]invitation.BulkItemResult, len(items))
	for i, item := range items {
		created, err := r.Create(ctx, actorID, eventID, item)
		switch {
		case err == nil:
			id := created.ID
			results[i] = invitation.BulkItemResult{Email: item.Email, Status: "created", InvitationID: &id}
		case errors.Is(err, domain.ErrConflict):
			results[i] = invitation.BulkItemResult{Email: item.Email, Status: "conflict"}
		case errors.Is(err, domain.ErrForbidden):
			results[i] = invitation.BulkItemResult{Email: item.Email, Status: "forbidden"}
		default:
			return invitation.BulkResult{}, fmt.Errorf("bulk-inviting %q: %w", item.Email, err)
		}
	}

	responseBody, err := json.Marshal(results)
	if err != nil {
		return invitation.BulkResult{}, fmt.Errorf("marshaling bulk invite result: %w", err)
	}

	const insertKey = `
		INSERT INTO idempotency_keys (actor_id, key, endpoint, request_hash, response_status, response_body)
		VALUES ($1, $2, $3, $4, 207, $5)
		ON CONFLICT (actor_id, key) DO NOTHING
		RETURNING id`
	var idempotencyRowID string
	err = r.pool.QueryRow(ctx, insertKey, actorID, idempotencyKey, bulkInviteEndpoint, requestHash, responseBody).Scan(&idempotencyRowID)
	if err == nil {
		return invitation.BulkResult{Items: results}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return invitation.BulkResult{}, fmt.Errorf("recording bulk invite idempotency key: %w", err)
	}

	// Lost the race: another call for this exact key committed its result
	// first, between this call's initial loadIdempotencyResult (which
	// found nothing) and this INSERT. Read back and return the winner's
	// stored result rather than this call's own.
	winner, found, err := r.loadIdempotencyResult(ctx, actorID, idempotencyKey, requestHash)
	if err != nil {
		return invitation.BulkResult{}, err
	}
	if !found {
		return invitation.BulkResult{}, fmt.Errorf("bulk invite idempotency key vanished after a lost race")
	}
	winner.Replayed = true
	return winner, nil
}

// loadIdempotencyResult looks up an existing idempotency_keys row for
// (actorID, key). found is false when no such row exists yet -- a
// genuinely first call. When a row exists, its request_hash is compared
// against requestHash: a match decodes and returns the stored result; a
// mismatch returns domain.ErrConflict (a key reused for a different
// request), never silently replaying or reprocessing.
func (r *InvitationRepository) loadIdempotencyResult(ctx context.Context, actorID, key, requestHash string) (invitation.BulkResult, bool, error) {
	const q = `SELECT request_hash, response_body FROM idempotency_keys WHERE actor_id = $1 AND key = $2`
	var storedHash string
	var responseBody []byte
	err := r.pool.QueryRow(ctx, q, actorID, key).Scan(&storedHash, &responseBody)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.BulkResult{}, false, nil
		}
		return invitation.BulkResult{}, false, fmt.Errorf("looking up idempotency key: %w", err)
	}
	if storedHash != requestHash {
		return invitation.BulkResult{}, false, domain.ErrConflict
	}

	var items []invitation.BulkItemResult
	if err := json.Unmarshal(responseBody, &items); err != nil {
		return invitation.BulkResult{}, false, fmt.Errorf("decoding stored bulk invite result: %w", err)
	}
	return invitation.BulkResult{Items: items}, true, nil
}
