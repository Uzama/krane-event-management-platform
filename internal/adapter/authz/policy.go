// Package authz implements the authz chokepoint's data half: the
// role_permissions table (domain/authz's Can interface) and the live
// per-event membership lookup that scopes every answer to one event.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domainauthz "github.com/Uzama/krane-event-management-platform/internal/domain/authz"
)

// knownRoles are the only values event_members.role and invitations.role
// can ever carry (migrations/20260823164421_init_schema.up.sql's CHECK
// constraints). A fourth role costs an INSERT into role_permissions plus an
// ALTER on those two CHECK constraints (D10, docs/requirements.md) -- update
// this slice alongside that ALTER, the same way Makefile's `token` target
// already hardcodes the three.
var knownRoles = []string{"admin", "contributor", "attendee"}

type permKey struct {
	role     string
	resource domainauthz.Resource
	action   domainauthz.Action
}

// Policy implements domain/authz.Policy.
//
// role_permissions is read-only to the API (item 02's GRANT SELECT) and
// changes only via a migration plus redeploy -- New loads it once at boot
// into an in-memory set and never refreshes it. That is deliberate: this is
// NOT dynamic, runtime-editable authorization, and item 24's ADR must say
// so plainly.
//
// Membership is the opposite choice, on purpose: Can queries event_members
// live on every call, never cached here alongside the permissions set. That
// is what makes "an admin removes a member and their very next request is
// denied" true without any cache-invalidation code -- item 09 is where that
// behaviour gets its own test.
type Policy struct {
	pool        *pgxpool.Pool
	permissions map[permKey]struct{}
}

// New loads the full role_permissions table and fails boot if any known
// role has zero permission rows. A role with no rows answers every
// question with (false, nil) -- indistinguishable from a legitimate deny --
// so an incomplete seed must fail loudly here, at boot, rather than surface
// later as silent fail-closed the first time someone hits it.
func New(ctx context.Context, pool *pgxpool.Pool) (*Policy, error) {
	permissions, err := loadPermissions(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("loading role_permissions: %w", err)
	}
	if err := validateRoleCoverage(permissions, knownRoles); err != nil {
		return nil, err
	}
	return &Policy{pool: pool, permissions: permissions}, nil
}

func loadPermissions(ctx context.Context, pool *pgxpool.Pool) (map[permKey]struct{}, error) {
	rows, err := pool.Query(ctx, `SELECT role, resource, action FROM role_permissions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	permissions := make(map[permKey]struct{})
	for rows.Next() {
		var role, resource, action string
		if err := rows.Scan(&role, &resource, &action); err != nil {
			return nil, err
		}
		permissions[permKey{role, domainauthz.Resource(resource), domainauthz.Action(action)}] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return permissions, nil
}

// validateRoleCoverage is the boot-time seed-completeness check: every
// known role must have at least one permission row, or New fails fast
// instead of silently starting with a role that can never do anything.
func validateRoleCoverage(permissions map[permKey]struct{}, roles []string) error {
	seen := make(map[string]bool, len(roles))
	for key := range permissions {
		seen[key.role] = true
	}
	for _, role := range roles {
		if !seen[role] {
			return fmt.Errorf("role_permissions has zero rows for role %q -- check the seed migration", role)
		}
	}
	return nil
}

// Can looks up userID's role on eventID, live, and checks it against the
// permissions loaded at boot. No event_members row -- whether because
// userID isn't a member of eventID or because eventID does not exist at
// all -- answers (false, nil) either way: a caller with no standing on an
// event has no standing to learn which of those two is true, so both cases
// must stay indistinguishable at every layer above this one.
func (p *Policy) Can(ctx context.Context, userID, eventID string, action domainauthz.Action, resource domainauthz.Resource) (bool, error) {
	const q = `SELECT role FROM event_members WHERE user_id = $1 AND event_id = $2`

	var role string
	err := p.pool.QueryRow(ctx, q, userID, eventID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("looking up membership: %w", err)
	}

	_, allowed := p.permissions[permKey{role, resource, action}]
	return allowed, nil
}
