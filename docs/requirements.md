# Requirement analysis

Feature 01 deliverable. Source of truth: [`assignment.md`](./assignment.md), plus the invariants in `CLAUDE.md`. Diagrams live beside this file: [`components.md`](./components.md) (architecture) and [`er-diagram.md`](./er-diagram.md) (data model).

The brief describes *what must be true*, not *how to build it* — choosing the mechanism is the exercise. This document records the entity model, the per-event role model, and the decisions taken so items 02–23 build against one vocabulary rather than re-deciding per feature.

**Track:** 1 — Backend (data correctness under load).

---

## 1. Domain in one paragraph

An **event** (conference, wedding, gathering) contains **sessions** scheduled inside it. Each session happens in a **room** and has a **speaker**. People hold **roles**, and roles are scoped to a single event — the same user is an admin on one event and an attendee on another. Events span time zones and DST boundaries, so every instant is stored as `timestamptz` and every event carries the IANA timezone name its local times should be rendered in.

---

## 2. Entities

Nine tables. Each row below names the invariant the table carries; §5 maps them back to `CLAUDE.md`.

### `users`

Global identity. Created on first sign-in by mapping the OIDC `sub` claim to a row — the API validates tokens, it never issues them.

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `default uuidv7()` — sortable, doubles as the keyset cursor |
| `subject` | `text` | the OIDC `sub` claim; **unique**; the only thing auth middleware looks up |
| `email` | `citext` | **unique**; the field an attendee must never see (item 10) |
| `name` | `text` | |
| `created_at` / `updated_at` | `timestamptz` | |

**No role column.** A global role would defeat the per-event model.

### `events`

The aggregate root. Rooms, sessions, members and invitations all hang off it.

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `uuidv7()` |
| `name` | `text` | |
| `description` | `text` | nullable |
| `timezone` | `text` | **IANA name** (`Asia/Colombo`, `America/New_York`). Never an offset. |
| `starts_at` / `ends_at` | `timestamptz` | absolute instants; rendered into `timezone` on the way out |
| `version` | `integer` | optimistic lock, item 17 |
| `deleted_at` | `timestamptz` | nullable — soft delete, so `audit_log` history stays resolvable |
| `created_at` / `updated_at` | `timestamptz` | |

### `event_members`

**The sole source of access.** If there is no row here, the user cannot see the event.

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `uuidv7()` |
| `event_id` | `uuid` | FK → `events`, `on delete cascade` |
| `user_id` | `uuid` | FK → `users` |
| `role` | `text` | `admin` \| `contributor` \| `attendee` |
| `created_at` / `updated_at` | `timestamptz` | |

**Unique `(event_id, user_id)`** — one role per person per event.

### `role_permissions`

The data behind the chokepoint (item 07). Adding a role is an INSERT, never a handler edit.

| column | type | notes |
|---|---|---|
| `role` | `text` | |
| `resource` | `text` | `event` \| `member` \| `room` \| `session` \| `invitation` |
| `action` | `text` | `create` \| `read` \| `update` \| `delete` \| `assign-role` |

**PK `(role, resource, action)`.** Presence of the row *is* the permission; there is no `allowed` boolean to get backwards.

### `rooms`

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `uuidv7()` |
| `event_id` | `uuid` | FK → `events` — rooms are per-event; see **D8** |
| `name` | `text` | unique within the event |
| `capacity` | `integer` | nullable |
| `version` | `integer` | item 17 |
| `created_at` / `updated_at` | `timestamptz` | |

### `sessions`

Carries the two hardest invariants: no double-booking, and correct local time across DST.

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `uuidv7()` |
| `event_id` | `uuid` | FK → `events` |
| `room_id` | `uuid` | FK → `rooms` |
| `speaker_id` | `uuid` | FK → **`users`** — an external speaker needs no roster row |
| `title` | `text` | |
| `description` | `text` | nullable — the `Optional[T]` PATCH proof case (item 20) |
| `time_range` | `tstzrange` | one column, not two — an `EXCLUDE` constraint needs a range |
| `version` | `integer` | item 17 |
| `deleted_at` | `timestamptz` | nullable |
| `created_at` / `updated_at` | `timestamptz` | |

Constraints (item 16):
- `EXCLUDE USING gist (room_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL)`
- `EXCLUDE USING gist (speaker_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL)`

Both are partial, so a soft-deleted session stops blocking its slot.

### `invitations`

An **independent record** of who was invited. It is *not* a pre-membership state machine — see §6.

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `uuidv7()` |
| `event_id` | `uuid` | FK → `events` |
| `user_id` | `uuid` | **nullable** FK → `users` — set when the invitee already exists |
| `email` | `citext` | always present; lets us invite people who have not signed up |
| `role` | `text` | the role being offered |
| `created_at` / `updated_at` | `timestamptz` | |

**Unique `(event_id, email)`** — the constraint that makes item 21's bulk invite naturally idempotent at the row level, independent of the `Idempotency-Key` replay.

No `status`, no `responded_at`. There is no accept/decline lifecycle.

### `audit_log`

Append-only. Written in the **same transaction** as the mutation it records.

| column | type | notes |
|---|---|---|
| `id` | `uuid` | PK, `uuidv7()` |
| `actor_id` | `uuid` | FK → `users` — **who**, taken from the authenticated `sub` |
| `entity_type` | `text` | `event` \| `room` \| `session` \| `event_member` \| `invitation` |
| `entity_id` | `uuid` | plain uuid, no FK — history outlives its subject |
| `action` | `text` | `create` \| `update` \| `delete` |
| `before` / `after` | `jsonb` | captured with PG18 `UPDATE … RETURNING OLD, NEW` |
| `request_id` | `text` | ties the row back to the request log |
| `created_at` | `timestamptz` | |

The application DB role is granted **INSERT and SELECT only**. Append-only is a grant, not a convention.

### `idempotency_keys`

| column | type | notes |
|---|---|---|
| `actor_id` | `uuid` | FK → `users` — keys are scoped per actor, not global |
| `key` | `text` | the client's `Idempotency-Key` header |
| `endpoint` | `text` | method + route |
| `request_hash` | `text` | detects the same key reused for a *different* body → 422 |
| `response_status` | `integer` | replayed verbatim |
| `response_body` | `jsonb` | replayed verbatim |
| `created_at` | `timestamptz` | |

**Unique `(actor_id, key)`.** The retry is deduplicated by the constraint, not by a `SELECT` first — check-then-act is exactly what the brief is testing for.

---

## 3. Relationships

```
users ──< event_members >── events ──< rooms
  │                            │         │
  │                            ├──< sessions >──┘   (room_id)
  └──────────────────────────────────┘             (speaker_id)

events ──< invitations >── users (nullable)
users ──< audit_log
users ──< idempotency_keys
```

| from | to | cardinality | notes |
|---|---|---|---|
| `events` | `event_members` | 1 : N | the roster |
| `users` | `event_members` | 1 : N | a user is a member of many events, with a different role in each |
| `events` | `rooms` | 1 : N | rooms are per-event, **D8** |
| `events` | `sessions` | 1 : N | |
| `rooms` | `sessions` | 1 : N | constrained by `EXCLUDE` — no two overlap |
| `users` | `sessions` | 1 : N | as speaker; constrained by `EXCLUDE` |
| `events` | `invitations` | 1 : N | 50k rows at seed scale |
| `users` | `invitations` | 1 : N | optional — an invitation may name a non-user |
| `users` | `audit_log` | 1 : N | as actor |

`users` is global. **Role is not** — it exists only on `event_members`. `invitations` sits *beside* membership, not upstream of it.

---

## 4. Per-event role model

From the brief: admin has full control and manages members and schedule; contributor manages sessions and invites attendees but cannot change roles; attendee reads events and sessions they are part of, nothing more.

Expressed as the `role_permissions` rows item 07 seeds:

| resource | action | admin | contributor | attendee |
|---|---|---|---|---|
| `event` | `read` | ✓ | ✓ | ✓ |
| `event` | `update` / `delete` | ✓ | — | — |
| `member` | `read` | ✓ | ✓ | — |
| `member` | `create` / `delete` | ✓ | — | — |
| `member` | **`assign-role`** | ✓ | **— (must 403)** | — |
| `room` | `create` / `read` / `update` / `delete` | ✓ | ✓ | — |
| `session` | `read` | ✓ | ✓ | ✓ |
| `session` | `create` / `update` / `delete` | ✓ | ✓ | — |
| `invitation` | `create` / `read` | ✓ | ✓ | — |

Three things this encodes:

1. **`assign-role` is its own action**, distinct from `member:update`. That is what makes item 07's `must:` test — a contributor gets 403 attempting a role change — a data question rather than a conditional in a handler.
2. **Authorization governs the response body, not just reachability.** An attendee may `event:read`, but the presenter for that role emits no roster and no `email` key anywhere (item 10).
3. **Access is membership.** Attendee read access means exactly *"has an `event_members` row for this event."* Item 10's list scoping filters by that membership — visibility is one half, scoping is the other. There is no accepted-vs-pending distinction and no read path that bypasses the chokepoint.
4. **A non-existent event and "not a member of this event" are the same answer, deliberately.** `Can` looks up the caller's row in `event_members` for the given event id; no row comes back whether the id belongs to no one or to no event at all, and both cases return the identical `(false, nil)` — surfaced as `403 forbidden`, never `404`. A `404` would tell a caller with no standing on an event whether that event exists, which is itself information the "no leak of what exists" rule (CLAUDE.md) says they aren't entitled to. Item 07 proves this at both the policy layer and the full HTTP stack.

---

## 5. Invariant map

Every correctness invariant in `CLAUDE.md`, and the column or constraint that enforces it. Nothing here is enforced in Go.

| Invariant | Mechanism | Where | Proved by |
|---|---|---|---|
| No check-then-act | every guarantee below is a constraint or an atomic statement | — | code review + the race tests |
| No double-booking | `EXCLUDE USING gist` on `(room_id, time_range)` and `(speaker_id, time_range)` | `sessions` | item 16 — two goroutines on a barrier; one 201, one 409 |
| Lost-update protection | `version integer`; `UPDATE … WHERE id = $1 AND version = $2`; 0 rows → 409 | `events`, `rooms`, `sessions` | item 17 — stale second write gets 409 |
| PATCH semantics | not a schema concern — `domain/opt.Optional[T]` in request DTOs, UPDATE built from set fields via goqu | `http/request`, `adapter/postgres` | item 20 — set / null / omit give three outcomes |
| Keyset pagination | `id uuidv7()` + `created_at`; opaque base64 cursor over `(created_at, id)`; no OFFSET | every listable table | item 19 — deep traversal with concurrent inserts |
| Query count independent of result size | FK shape supports `WHERE id = ANY($1)` batching and JOIN-and-stitch | `sessions` → `rooms` / `users` | item 18 — pgx tracer, equal counts for 5 vs 500 |
| Idempotent writes | unique `(actor_id, key)`; plus unique `(event_id, email)` on the row itself | `idempotency_keys`, `invitations` | item 21 — retry replays, never double-sends |
| Audit is append-only | INSERT/SELECT grants only; row written in the mutation's transaction; `before`/`after` from `RETURNING OLD, NEW` | `audit_log` | item 22 — actor + before + after recorded; log cannot be mutated |
| Time is `timestamptz` | every timestamp `timestamptz`; `events.timezone` holds an IANA name; sessions stored as `tstzrange` | all tables | item 12 — DST test across the event's boundary |
| Per-event authorization | role lives only on `event_members`; permissions are `role_permissions` rows | `event_members`, `role_permissions` | item 07 — contributor 403 on role change |

---

## 6. Decisions taken

Recorded here so items 02–23 do not re-litigate them.

| # | Decision | Rejected alternative | Why |
|---|---|---|---|
| D1 | Invitations are an independent record; membership is populated directly by admins and the seed script | invitation → accept → member lifecycle | The lifecycle adds a read path outside the per-event chokepoint for marginal domain value in a 15-hour budget. Cut deliberately — see §8. |
| D2 | `invitations` carries a nullable `user_id` **and** an `email` | `user_id` only | An invitation whose recipient must already have an account is not an invitation. |
| D3 | `sessions.speaker_id` → `users`, any user | FK to `event_members` | An outside speaker should not need a roster row first. The tighter FK blocks the ordinary case. |
| D4 | Soft delete (`deleted_at`) on `events` and `sessions` | hard delete | `audit_log` before/after history stays resolvable. Partial `EXCLUDE` indexes keep deleted sessions from blocking slots. |
| D5 | Session time is one `tstzrange`, not `starts_at` + `ends_at` | two columns | An `EXCLUDE` constraint needs a range type. Two columns push the check back into Go. |
| D6 | `role_permissions` presence *is* permission | a boolean `allowed` column | A three-valued permission table is a bug waiting to be read backwards. |
| D7 | Mermaid-in-Markdown for both diagrams | rendered PNG/SVG, dbml | Renders on GitHub, diffs as text, and item 27's refresh is a text edit. |
| D8 | **Rooms are per-event.** `rooms.event_id` FK → `events`. Cross-event physical-room conflicts are **out of scope** | shared physical rooms with an `event_room_usage` join table | Keeps item 16's `EXCLUDE` clean and honest — the constraint covers exactly the conflicts the model claims to have. See §7.1. |
| D9 | **Soft-deleting a session releases its room and speaker slot immediately.** Item 16's `EXCLUDE` constraints are partial (`WHERE deleted_at IS NULL`) | a non-partial `EXCLUDE`, where a cancelled session keeps blocking its room forever | Cancelling a session should free the slot; the row stays so `audit_log` history resolves. Chosen, not inherited from the partial index — see §7.3. |
| D10 | `event_members.role` and `invitations.role` carry `CHECK (role IN ('admin','contributor','attendee'))` | no CHECK, with `role_permissions` as the sole registry | A typo'd role is rejected at write time rather than silently granting nothing. Cost: a fourth role is `INSERT`s into `role_permissions` **plus** one `ALTER` — a migration, never a handler edit, so the chokepoint invariant holds. `role_permissions.role` is deliberately left unconstrained: it *is* the registry. For item 24's ADR. |
| D11 | `user_email` visibility on `event_members` responses is **admin-only**; contributor sees the roster (names, roles) but never email | contributor sees emails only of people they personally invited (provenance-based) | Admin manages *membership*, so needs email to identify/contact members directly. Contributor manages the *schedule* (sessions, speakers), so needs names to assign work, but has no operational need to read email back — `member:create` (admin-only anyway) takes an email as input, never surfaces one on read. The provenance-based alternative requires tracking invite-authorship per `event_members` row for marginal benefit, and doesn't cover pre-existing roster members a contributor didn't add — a clean role-based cut beats a provenance-based one. Resolved during item 10's build rather than emailed (§7.2); staged in §8 pending item 26's `TRADEOFFS.md`. For item 24's ADR. |

---

## 7. Scope boundaries and open questions

### 7.1 Decided — rooms are per-event, cross-event conflicts out of scope (D8)

A room belongs to exactly one event: `rooms.event_id` FK → `events`. Two events running in the same physical hall are, in this model, two `rooms` rows, and the platform will not detect a clash between them.

**The alternative, and why it was rejected.** Modelling shared physical rooms is arguably more correct — real venues rent the same hall to concurrent events. But it costs:

- a separate `venues`/`rooms` scope, with `rooms` no longer hanging off `events`;
- an `event_room_usage` join table to express which events may book which rooms;
- an extra join on **every** session query, list and read alike, plus a validation that `sessions.room_id` is a room the event is entitled to.

**Why per-event wins here.** Item 16's `EXCLUDE USING gist (room_id WITH =, time_range WITH &&)` covers exactly the conflicts this model claims to have — within an event, a room cannot be double-booked, and the constraint catches it in the `INSERT`. That is honest. The shared-room model would leave a class of conflict the constraint provably cannot see unless the join table is *also* constrained, which multiplies the surface the depth track is graded on for a scenario the brief never asks about. The brief says "a room or speaker can't be double-booked" and rewards deliberate cuts over completeness.

**What this costs, stated plainly.** Two events in the same building can double-book Hall A and the API will accept both. That is a known, documented limitation, not an oversight — recorded in §8 for `TRADEOFFS.md`.

This resolves what was previously flagged as blocking item 02. The migration can proceed.

### 7.2 Decided (D11) — a contributor sees the roster, never attendee emails

Flagged at feature 01 as the one open question — the brief invites clarifying questions at `engineering@krane.tech` and calls asking before building "the behavior we want." A draft email was written (§7.4) but never sent; item 10 resolved it during the build instead of blocking on a reply, and records that here as a judgment call, not something the brief settled (see §8 for the staged `TRADEOFFS.md` entry).

The brief says an *attendee* must never see the roster or anyone's email, and that is unambiguous and tested (item 10). A contributor invites attendees, which implies seeing at least the invite list. What was not stated is whether the email ban stops at attendee or extends to everyone below admin.

**Decision (D11, §6):** contributors may `member:read` and see the roster (names, roles); `user_email` is admin-only. Rationale: admin manages *membership* and needs email to identify/contact members directly; contributor manages the *schedule* and needs names to assign work, with no operational need to read email back — inviting someone (`member:create`, admin-only regardless) takes an email as input, it never surfaces one on read. Rejected alternative: a contributor sees emails only of people they personally invited — rejected because it needs invite-authorship tracked per `event_members` row for marginal benefit, and doesn't cover pre-existing roster members the contributor didn't add. The item 10 `must:` test asserts the attendee case, which is the one the brief names outright — D11 extends the same principle one role further down, deliberately, rather than leaving it unstated.

### 7.3 Decided — cancelling a session frees its slot at once (D9)

Item 16's exclusion constraints are partial: `EXCLUDE USING gist (room_id WITH =, time_range WITH &&) WHERE (deleted_at IS NULL)`. That is a **product rule**, stated here so it is implemented deliberately rather than inherited as a side effect of the `WHERE` clause:

> Cancelling a session frees its room and its speaker for that slot immediately. The slot is rebookable the moment the session is soft-deleted, and the cancelled row remains so `audit_log` history stays resolvable. There is no hold period, and restoring a soft-deleted session whose slot has since been rebooked is **not supported** — the restore would violate the constraint and return 409.

The alternative, a non-partial `EXCLUDE`, would let a cancelled session block its room forever. That is worse, but it was a choice, and item 16 implements the rule above rather than discovering it.

### 7.4 Draft email (not sent)

> Subject: Event Management take-home — one clarification
>
> Hi,
>
> One question before I finalise the response presenters. The brief is explicit that an attendee never sees the roster or anyone's email, and I've tested that. Does the email ban extend to contributors, who invite attendees and presumably need to see who's on the list — or does it stop at attendee?
>
> I'm proceeding on the assumption that contributors see the roster but not emails, and that only admins see email addresses. Flagging it since it's load-bearing for the field-visibility rules.
>
> Separately, for the record rather than as a question: I've scoped rooms to a single event, so cross-event conflicts on the same physical space aren't modelled. It keeps the exclusion constraint covering exactly what the schema claims. Happy to revisit if shared venues were intended.
>
> Thanks,
> Uzama

---

## 8. Cuts, for `TRADEOFFS.md` (item 26)

Entries below are staged here because `TRADEOFFS.md` does not exist yet (item 26 creates it). **Item 26 must migrate every entry in this section into `TRADEOFFS.md` verbatim (or better), not just the ones written before it** — each already carries the decision, what was rejected, why, and a "two more weeks" line, so the migration is a copy, not a rewrite. Do not let a cut recorded here get silently dropped because it wasn't the most recent one when item 26 ran.

> **Cut the invitation acceptance lifecycle.** Invitations exist to satisfy idempotent bulk invite (item 21) and to model inviting non-users; membership is populated directly by admins and the seed script. The lifecycle added a read path outside the per-event authz chokepoint for marginal domain value in a 15-hour budget. Two more weeks: add accept/decline and invitation→membership conversion.

> **Scoped rooms to a single event; cross-event physical-room conflicts are out of scope.** Shared physical rooms are more faithful to real venues, but they require a separate room scope, an `event_room_usage` join table, and an extra join on every session query — and they leave a conflict class the `EXCLUDE` constraint cannot see without further constraints of its own. Per-event rooms keep item 16's constraint covering exactly what the model claims. Known limitation: two events in the same building can both book Hall A. Two more weeks: a venue scope with room entitlements, and an exclusion constraint over physical rooms rather than per-event rows.

> **Item 09's last-admin protection is a locking protocol, not a database constraint.** `MemberRepository.AssignRole`/`Delete` take `SELECT ... FROM events WHERE id=$1 FOR UPDATE` before any write that could change an event's admin count, serializing concurrent admin-count-affecting writes so the guard's `EXISTS` check runs against a fresh, consistent count — proven both by an outcome-based race test and by a second test that deterministically forces the vulnerable window open via a test-only synchronization seam (`AfterEventLockForTest`) and asserts no two writers are ever inside the post-lock section concurrently. The gap: nothing but that convention (documented in `FAILURES.md`) stops a future write path on `event_members` from skipping the lock and silently reopening the race — the database itself does not enforce "every event has at least one admin." Two more weeks: a deferred constraint trigger asserting `count(*) FILTER (WHERE role='admin') >= 1` per event on `event_members`, which would be violation-proof against any future code path regardless of whether it remembers the locking convention.

> **Resolved member-email visibility (D11) during item 10's build rather than waiting on the never-sent §7.2 email.** `docs/requirements.md` §7.2 flagged, at feature 01, whether the attendee email ban extends to contributors; a draft to `engineering@krane.tech` was written but never sent. Rather than block item 10 on a reply, it was decided during the build: email is admin-only PII (D11) — admin manages membership and needs it to identify/contact members, contributor manages the schedule and needs names, not email, and the invite flow only ever takes an email as input. Rejected: a provenance-based rule where a contributor sees emails only of people they personally invited — it needs invite-authorship tracked per `event_members` row for marginal benefit over a role-based cut, and doesn't cover pre-existing roster members a contributor didn't add. This is a judgment call on an ambiguous requirement, recorded as that. Two more weeks: if real usage showed contributors needed contact info, add a narrower `invitation:read`-scoped view rather than widening `member:read`.

---

## 9. What this feature did not decide

Deliberately deferred, with the item that owns each:

- Router, codegen wiring, and the exact OpenAPI shape — items 03–05.
- The mock OIDC issuer and how demo tokens are minted for `make seed` — items 03, 06.
- Cursor encoding format — item 19.
- Recurring-session rules and exceptions — item 23; the schema above has no `recurrence` column yet on purpose.
- Whether the agent reads through the API with a user's token — item 15 (it does; the brief requires it).
