# CLAUDE.md

Event Management Platform — a Go REST/JSON API over Postgres 18, with a thin read-only agent. The system schedules sessions inside events, in rooms, with people who hold per-event roles.

`make up && make seed && make test` must pass on a clean machine with Docker in under 5 minutes. These three targets are the contract; keep them green from the first commit onward.

Known repo mistakes and gotchas: @FAILURES.md — read it before starting any task.

> **Partially scaffolded.** `go.mod`, `Makefile`, `docker-compose.yml`, `migrations/` and CI exist, and `make up/seed/test/lint` all run green. The `internal/` tree below is still the *target* layout — its packages arrive with feature 04 in `FEATURES.md`, and `make seed` is a stub until feature 14. Delete this note once 04 lands.

## What this repo is — clean architecture

Dependencies point inward to `domain`. The domain knows nothing about the outer layers; everything else depends on it. Interfaces live in `domain`; `adapter` implements them. `container` wires concrete adapters into domain services and into `http`; `bootstrap` runs it; `cmd` just calls `bootstrap`.

Import direction: `cmd → bootstrap → container → {http, adapter} → domain`. `utils` is leaf-only — any layer may import it, and it imports nothing inner.

```
cmd/
  api/main.go           # entrypoint — calls bootstrap.Boot()
  agent/main.go         # thin read-only CLI agent (an API client; not part of the layered core)
  seed/main.go          # seed generator: 50 events / 5k users / 50k invitations, crosses a DST boundary
internal/
  domain/               # THE CORE. No framework imports (no pgx, net/http, goqu).
    <aggregate>/        # event, session, room, member, invitation
      entity.go         # the type + its invariants
      service.go        # business logic / use cases for that aggregate
      port.go           # interfaces it needs (repository, policy) — implemented in adapter
    errors.go           # ErrConflict, ErrVersionMismatch, ErrForbidden, ErrNotFound
    opt/                # Optional[T] presence type for partial updates (leaf; used by services + request DTOs)
  adapter/              # implements domain ports
    postgres/           # repositories (pgx + goqu); map constraint/version violations → domain errors; audit in same tx
    auth/               # JWT validation via JWKS; maps the sub claim to a domain user
    authz/              # loads role_permissions; backs the domain authorization policy
  http/                 # delivery — depends on domain interfaces only, never on adapter
    server.go           # http.Server construction, timeouts, graceful-shutdown hook
    router.go           # the API surface: routes → handlers
    middleware/         # request-id, recover, auth (validate JWT), authz (the chokepoint), idempotency
    handler/            # one per aggregate; thin — decode, call service, encode
    request/            # request DTOs (use domain/opt for nullable PATCH fields)
    response/           # response DTOs + per-role presenters (field visibility)
    validator/          # validates requests/responses against the OpenAPI contract
  utils/                # LEAF helpers only — no inner imports
    config.go           # env → typed config, with defaults
    logger.go           # structured logger
    db.go               # pgx pool constructor
    cursor.go           # opaque keyset-cursor encode/decode
  container/            # composition root: build adapters, inject into services, into handlers
  bootstrap/            # Boot(): load config → build container → mount router → listen → graceful shutdown
migrations/             # timestamped .sql, append-only; EXCLUDE constraints, uuidv7 defaults, audit grants live here
openapi/                # the contract; the validator checks the server against it
```

Root also holds `docker-compose.yml` (Postgres 18 + mock OIDC), `Makefile` (up/seed/test/lint), and the docs (`AI-WORKFLOW.md`, `TRADEOFFS.md`, ADRs, design diagram).

Rules:
- Each layer has one job: `domain` declares `ErrConflict`; `adapter/postgres` detects the `EXCLUDE`/version violation and returns that domain error; `http` maps it to 409. No layer reaches around another.
- `http` and `adapter` both depend on `domain` and never on each other. They meet only in `container`.
- Interfaces are defined in `domain` (with the service that consumes them) and implemented in `adapter`. Handlers hold service interfaces, not concrete types.
- The authz chokepoint spans layers, one concern per place: policy interface in `domain`, permission data in `adapter/authz`, enforcement in `http/middleware`, body filtering in `http/response` presenters.
- `container` may import everything; nothing may import `container`. Never put wiring in `utils` — a leaf that inner layers import cannot also import them back.
- The agent and seed are separate binaries under `cmd/`, not part of the core.
- Keep it pragmatic: map DTO↔domain only at the http and postgres edges. Nothing new at the repo root without asking.

## Ask before you assume

Never guess at intent. If a task leaves anything open — which endpoint, what happens on failure, whether it needs a migration, what the response shape is per role — stop and use the **AskUserQuestion** tool. One question up front is cheaper than half a day in the wrong direction.

- Ask when a request could reasonably mean two things.
- Ask before changing the OpenAPI contract, a DB schema, or an authz rule.
- Do not invent product decisions, acceptance criteria, or error semantics.
- Do not widen scope past what was asked. Note the adjacent thing you spotted; don't fix it unprompted.
- If you had to assume something you couldn't resolve, list it at the top of your summary.

## Correctness invariants — never violate

Push every guarantee into Postgres; never trust check-then-act in Go.

- **No check-then-act.** A `SELECT` to check a condition followed by a write that depends on it races. Use a constraint or an atomic statement instead. If you write this pattern, stop and flag it.
- **No double-booking.** Enforced by a PG18 `EXCLUDE` / temporal constraint on `(room_id, tstzrange)` and `(speaker_id, tstzrange)`, not application logic. Constraint violation → 409.
- **Lost-update protection.** Every mutable row has a `version` column. Updates are `WHERE id = $ AND version = $`; 0 rows affected → 409 with the current state. Never last-write-wins.
- **PATCH semantics.** An absent field and an explicit `null` are different requests. Never model both as a plain `*string`. Use a presence-tracking `Optional[T]`. Build the UPDATE from only the fields that were set (goqu).
- **Pagination is keyset only.** Order by `(created_at, id)` or uuidv7 id, opaque base64 cursor. Never `OFFSET`.
- **Query count independent of result size.** No N+1 — never fetch related rows in a loop. Batch with `WHERE id = ANY($1)` or JOIN/aggregate and stitch in Go. Prove it with a query-counting test.
- **Idempotent writes.** Bulk/mutating operations honour an `Idempotency-Key` backed by a unique constraint. Retries replay the stored result; they never double-send. Partial failure returns a defined per-item result, not an accident.
- **Audit is append-only.** Write the audit row in the *same transaction* as the mutation. Capture before/after with `UPDATE ... RETURNING OLD, NEW` (PG18). The DB role has INSERT/SELECT on `audit_log` only — no UPDATE/DELETE grant.
- **Time is timestamptz.** Every event stores an IANA timezone name (`Asia/Colombo`, `America/New_York`). Never naive timestamps, never fixed UTC offsets. Seed data crosses a DST boundary.

## Authorization

- One chokepoint answers `can(user, action, resource)` by reading a `role_permissions` table. Roles are **per-event**, not global. Adding a role means inserting rows, never editing a handler.
- Authorization governs the **response body**, not just which endpoints are reachable. An attendee reading an event must never receive the roster or anyone's email. Field visibility is role-driven.
- A test asserts an attendee's JSON contains no `email` key anywhere. Do not delete or weaken it.

## Auth

- Off-the-shelf only. The API **validates** JWTs (signature via JWKS, expiry, audience) and maps the `sub` claim to a user row. It never issues, signs, or mints tokens.
- Never generate a `/login` endpoint that signs tokens with a shared secret. That is hand-rolled auth and is forbidden. If you find yourself writing one, stop and flag it.
- Local dev/test uses a mock OIDC issuer in docker-compose; the same validation code points at a hosted issuer in prod by changing one env var.

## Project files (what goes where)

These four files are living state. Keep each to its job; do not let one absorb another.

- `FEATURES.md` — the work queue: small features, dependency-ordered, each with a status. Pick the next unstarted one.
- `AUDIT.md` — append-only activity log; one timestamped entry per completed feature or fix, describing what was done.
- `ISSUE.md` — bug ledger: for each runtime bug, its root cause and the solution. One narrative entry per bug.
- `FAILURES.md` — permanent imperative rules ("never do X"), auto-loaded every session via `@FAILURES.md`. One line each.

`FAILURES.md` vs `ISSUE.md`: a bug in the running system goes in `ISSUE.md`. A lesson that should change future behaviour goes in `FAILURES.md`. One bug often produces both — an `ISSUE.md` entry describing what broke, and a one-line `FAILURES.md` entry so it never recurs.

## The feature workflow

Work is a queue in `FEATURES.md`: small features, ordered by dependency, each with a status. On a new session, read `FEATURES.md` and pick the next unstarted feature. When a feature is fully done, mark it done there.

Every feature runs through this loop. Do not skip or reorder steps. Do not bypass a human gate.

1. **Plan (plan mode).** Read the relevant requirement, the invariants above, and `@FAILURES.md` so past mistakes aren't repeated. Use **AskUserQuestion** to resolve every ambiguity — no assumptions.
2. **Plan review — STOP.** Present the plan as a table for sign-off: `What | Before → After | Why | How`. State which invariants apply and how the plan satisfies each. Wait for approval before writing code.
3. **Branch, then implement test-first (TDD).** Cut the feature branch off `main` before the first edit (see **Git**). Write the failing test first; watch it fail for the right reason; then make it pass. Keep each change small enough for a human to review in one sitting. If it's too big, break it into tasks and do them one at a time — all on the same branch.
4. **Green is mandatory.** Run `make test`. Never report success on a red loop. Never `t.Skip`, comment out, or weaken a test to reach green. If a test is wrong, say so and explain why before touching it.
5. **Implementation review — STOP.** Present changes as a table: `File | Change | Why | Requirement it satisfies`. Self-audit: for each applicable invariant, name the line that enforces it and the test that proves it. List anything you were unsure about or deviated from. Wait for review.
6. **Remaining tasks.** If the feature has more tasks, return to step 3 for the next one.
7. **Log before committing.** Append a timestamped entry to `AUDIT.md` describing the work done, and — only if the feature is fully finished — mark it done in `FEATURES.md`. These land in the same commit as the work, never in a later one.
8. **Commit — STOP.** When all tasks are done, show the proposed commit message for review. Wait for confirmation.
9. **Commit** only after confirmation — on the feature branch, never on `main`. Stage the `AUDIT.md` and `FEATURES.md` updates from step 7 along with the change.
10. Stop and hand the branch over. The human merges; the next feature branches off `main` after they have.

## The bug workflow

1. **Root cause first.** Diagnose from the given information. If you need more, use **AskUserQuestion** — do not guess and patch symptoms.
2. **Solution review — STOP.** Describe the root cause and the proposed fix. Wait for approval.
3. **Implement** the fix on a `fix/<kebab-slug>` branch (see **Git**). Tests must be green, including a test that reproduces the bug.
4. **Fix review — STOP.** Show the change as a table. Wait for review.
5. **Log before committing.** Append root cause + solution to `ISSUE.md`, and a timestamped entry to `AUDIT.md`. If the fix reveals a rule that wasn't written down, add the one-line imperative to `FAILURES.md` now too.
6. **Commit — STOP.** Show the commit message. Wait for confirmation.
7. **Commit** only after confirmation — on the `fix/` branch, with the step-5 log entries staged alongside the fix.

## Human gates are absolute

- Always stop at a review gate. Never bypass one, never assume approval, never continue past a STOP.
- The human commits the decision; you propose. Do not run `git commit` before the message is confirmed.
- If the human asks for a change, make only that change — don't widen it.
- Stay on the current task. Don't drift into adjacent work.
- Before starting any task or bug, read `@FAILURES.md` and `ISSUE.md`; do not repeat a logged mistake.
- Use skills when one applies.

## Git

- **One branch per feature.** Branch off `main` before the first edit of a `FEATURES.md` item: `feature/<nn>-<kebab-slug>` — e.g. `feature/01-er-diagram`. Bugs use `fix/<kebab-slug>`. Every task and commit for that item lives on that branch.
- **Never touch `main`.** Do not commit to it, merge into it, or push. Work stops when the branch is committed; the human reviews and merges. Do not start the next feature until they have.
- **Commit messages are point form and high level.** A short subject line, then bullets. Each bullet says *what changed and why it matters* — not how, not which files. The diff already carries the detail; don't restate it. If a bullet only makes sense next to the diff, cut it.
- When a commit fixes something you got wrong, say so in the message.

## The loop (commands)

A task is not done until these are green.

```bash
make up            # postgres 18 + mock OIDC, migrated. IDEMPOTENT — safe to re-run
make seed          # 50 events, 5k users, 50k invitations; prints demo tokens
make test          # go test ./... against real Postgres in Docker (never mocks for repo/integration)
make lint          # gofmt/vet/golangci-lint
make down          # stop everything and DELETE all data — the ONLY destructive target
```

- **`make up` must stay idempotent, and `make down` is the only thing that removes data.** `make test` depends on `up`, so an `up` that wiped volumes would destroy what `seed` just wrote before `test` ever saw it — at feature 14 that is 50k invitations vanishing silently mid-chain. Re-running `up` is always safe.

- Run `make test` after every meaningful edit, not once at the end.
- To iterate on one failing test, `make up` first (the suite needs Postgres), then run it directly: `go test ./internal/domain/session/ -run TestCreateSession -race -count=1`.
- Tests run against real Postgres, not mocks. Repo and integration tests use a throwaway database (`krane_test`), dropped and recreated by `make test`.
- **No test may assume it is the only occupant of the database.** Create your own fixtures with unique ids; never `TRUNCATE` a shared table. Isolation today is one database for the whole run; at the first package that writes data it becomes `CREATE DATABASE <pkg> TEMPLATE krane_test` per package, and only tests that obey this rule survive that change unedited.
- Isolation is **per-package, never per-test**. Feature 16's race test needs two goroutines contending on the same rows through the same constraint — isolating them from each other would destroy the thing it exists to prove. Concurrency tests share a database by design.
- Do not start the server and leave it running to "verify" a change — it never exits. Use `make test`.
- For concurrency, write the failing test first and confirm it actually races (goroutines released together on a barrier), not sequential calls that can't catch the bug. Always run these with `-race`.

## Stack (fixed — do not substitute)

- Go 1.23, REST/JSON, OpenAPI via oapi-codegen, goqu, sqlx + pgx, golang-migrate.
- PostgreSQL 18 — required, not a floor. Prefer PG18-native features: `EXCLUDE`/temporal constraints, `RETURNING OLD/NEW`, `uuidv7()`.
- Ask before adding any dependency. Prefer the standard library. Exact versions only, lockfile committed. Nothing single-maintainer or freshly published for anything touching auth, crypto, or networking.

## Error handling

- No ignored errors. Handle it meaningfully or return it. Never swallow.
- Every error response uses one envelope with a stable machine-readable code:

```json
{ "error": { "code": "session_conflict", "message": "That room is already booked for this slot.", "details": {} } }
```

- Validation failures → 422 with the issues in `details`. Version/booking conflicts → 409. Missing permission → 403 with no leak of what exists.
- Log with structured context (`request_id`, `actor_id`, `entity_id`), never a bare string.

## Naming — one word per concept

| Concept              | Use                    | Never                       |
| -------------------- | ---------------------- | --------------------------- |
| A scheduled item     | `session`              | talk, slot, meeting         |
| The container        | `event`                | conference, gathering       |
| A place it happens   | `room`                 | venue, hall, location       |
| Membership + role    | `event_member`         | participant, attendee row   |
| Account access       | `Sign in` / `Sign out` | Login, Log In               |

- Functions: `createSession`, `getSession`, `listSessions`, `updateSession`. Not `fetch`, `save`, `handle`.
- Booleans read as assertions: `isBooked`, `hasConflict`, `canEdit`.
- Routes: kebab-case, plural nouns — `GET /v1/events/:eventId/sessions`.
- Postgres: snake_case, plural tables — `events`, `sessions`, `started_at`.
- Test files sit beside the source: `sessions/service.go` → `sessions/service_test.go`.

## Keeping context current

When you make a mistake, get corrected, or learn something about this repo that wasn't written down, add one imperative line to `FAILURES.md` in the same commit and mention it in your summary. Keep this file focused — it loads into every session, and long context makes the model less reliable, not more.
