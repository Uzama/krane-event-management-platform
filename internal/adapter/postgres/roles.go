package postgres

// canGrantRoleActorCTE and canGrantRoleGuard implement the single
// escalation rule shared by every write path that lets one actor grant
// another person a per-event role: only an admin may grant/invite anything
// but attendee (domain/member.Repository.Create's doc comment states this
// as the domain contract; these two fragments are its SQL enforcement,
// nothing more). Reused verbatim by MemberRepository.Create (item 09) and
// InvitationRepository.Create (item 13) so the two paths cannot drift.
//
// Callers must issue their write as a CTE named "actor":
//
//	WITH actor AS (canGrantRoleActorCTE) ...
//
// with query parameters ordered (eventID, _, role, actorID) at positions
// $1, $2, $3, $4 respectively (the second parameter's identity doesn't
// matter to this fragment -- it's whatever the caller's own write needs at
// that position, e.g. the target email), and gate the write's SELECT/WHERE
// on canGrantRoleGuard.
//
// canGrantRoleGuard fails CLOSED: if actor has no membership row on this
// event at all (the EXISTS(SELECT 1 FROM actor) term), every grant is
// denied -- including a grant of "attendee", which the second clause alone
// would otherwise allow for a non-member. This repository-level guard is
// defense-in-depth behind the http/middleware Authz chokepoint (which
// already requires actorID to hold member:create or invitation:create on
// eventID before either write path is ever reached in production); it must
// still deny everything on its own if that chokepoint is ever bypassed,
// misconfigured, or called around -- never rely on the caller having
// already verified membership.
const canGrantRoleActorCTE = `SELECT role FROM event_members WHERE event_id = $1 AND user_id = $4`

const canGrantRoleGuard = `(EXISTS (SELECT 1 FROM actor) AND ($3 = 'attendee' OR EXISTS (SELECT 1 FROM actor WHERE actor.role = 'admin')))`
