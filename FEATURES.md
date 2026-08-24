# FEATURES.md

The work queue. Features are small and dependency-ordered: on a new session, pick the first unchecked item and run it through the feature workflow in `CLAUDE.md`. Mark it done here when it is fully committed and logged.

Status: `[ ]` todo · `[~]` in progress · `[x]` done. Each item lists its scope, what it depends on, and — where the assignment demands one — the specific test that proves it (`must:`).

---

## Phase 0 — Project init

- [x] **00 · git-ai + repo skeleton.** `git init`, `.gitignore`, `CLAUDE.md`, `FAILURES.md`, `FEATURES.md`, `AUDIT.md`, `ISSUE.md`, and `go.mod` (module `github.com/Uzama/krane-event-management-platform`, Go 1.23.0). git-ai capture verified live — `git-ai status` attributes checkpoints to both human and model. Commit `5e9bf10`.

## Phase 1 — Design & scaffolding

- [x] **01 · Requirement analysis + ER diagram.** Read the assignment closely; list entities, relationships, and the per-event role model. Produce **both halves of the required design diagram**: a components diagram (the clean-architecture layers and how a request flows through them) and an ER diagram (the data model). Note open questions and email `engineering@krane.tech` if anything is genuinely ambiguous. Deliverables: `docs/components.md`, `docs/er-diagram.md`, plus `docs/requirements.md` for the written analysis. Refreshed to match what shipped in 27.
- [x] **02 · DDL / schema migrations.** Write the initial migration(s): `users`, `events`, `event_members`, `role_permissions`, `rooms`, `sessions`, `invitations`, `audit_log`, `idempotency_keys`. All timestamps `timestamptz`; events carry an IANA timezone column; ids `uuidv7()`. Defer the EXCLUDE/version columns to their depth features or add them now with a note. needs: 01. Shipped as one timestamped up/down pair. `version`/`deleted_at` columns are in; both `EXCLUDE` constraints are deferred to 16 so its race test can fail first. Adds the `krane_migrator`/`krane_app` split — **item 03's compose must set `POSTGRES_USER=krane_migrator`** and point the API DSN at `krane_app`.
- [x] **03 · docker-compose + Makefile + green pipeline.** `docker-compose.yml` with Postgres 18 (pinned) and a mock OIDC issuer. `Makefile` targets `up`, `seed` (stub for now), `test`, `lint`. Get `make up && make seed && make test` passing with one trivial test. Add `.github/workflows/ci.yml` running `make lint && make test` on push — CI exists from here on, and later items extend it. needs: 02. Shipped: pinned `postgres:18.6` + `mock-oauth2-server`, one-shot `migrate` and `app-role` services, `krane_test` throwaway DB. Graded chain green in 1m25s cold / 14s warm. **`make up` is idempotent — `make down` is the only destructive target.** **Item 04 must add the `api` service to `docker-compose.yml`**; CI has not executed yet, as the branch is unpushed.
- [x] **04 · Repo architecture scaffolding.** Create the clean-architecture packages (`domain`, `adapter`, `http`, `utils`, `container`, `bootstrap`) per `CLAUDE.md`. Wire `bootstrap.Boot()`, the container, config loading, db pool, structured logger, and a `GET /health` endpoint end to end. needs: 03. Shipped: `utils`/`container`/`http`/`bootstrap`/`cmd/api` wired end to end; `domain`/`adapter` deliberately left empty (no consumer until 06-08/12/20). Health is a readiness check (pings the db pool via a local `Pinger` interface, never `pgx` directly) using the standard error envelope on 503. `api` added to `docker-compose.yml` but not in `make up`'s default set. **Item 05 must verify oapi-codegen targets `net/http.ServeMux` before writing code, or reopen the router choice.**

## Phase 2 — Foundation API

- [x] **05 · OpenAPI harness.** Establish the contract-first loop: an `openapi/` spec, `oapi-codegen` for types/interfaces, and request/response validation wired into tests. Add the contract check to the CI workflow from 03 — the brief requires CI to verify the server honours the spec, so a drifted spec must fail the build, not just a local run. From here, every endpoint updates the spec first. needs: 04. Shipped: `openapi/openapi.yaml` (3.0.3, `GET /health` only), oapi-codegen's `std-http-server` target confirmed against stdlib `ServeMux` (item 04's open question), `internal/http/validator` wraps kin-openapi for test-only validation, `make contract-check` (generate + `git diff --exit-code`) wired into CI before `make test`. Go toolchain, pgx, golangci-lint, and Docker images all untouched — a considered Go-1.25 bump was rejected as out of scope.
- [ ] **06 · Auth (validate-only).** JWKS-based JWT validation middleware; map `sub` → user row; wire the mock OIDC issuer; add `make token USER=…` (or have `make seed` print demo tokens for an admin, a contributor, an attendee). Never issue/sign tokens in the API. needs: 04.
- [ ] **07 · Authorization chokepoint.** `role_permissions` table + a `can(user, action, resource)` policy (interface in `domain`, data loaded by `adapter/authz`, enforced in `http/middleware`). Per-event roles. Adding a role = inserting rows, no handler edits. Seed the permission matrix the brief specifies: admin = full control incl. member/role management; contributor = manages sessions and invites attendees but **cannot change roles**; attendee = read-only on events and sessions they belong to. must: a test proving a contributor gets 403 attempting a role change. needs: 06.
- [ ] **08 · Events CRUD.** Create/read/list/update/delete events through the full stack, each route behind the chokepoint. Establishes the handler→service→repo pattern. needs: 05, 07.
- [ ] **09 · Event membership + roles.** Add/remove members, assign per-event roles (admin/contributor/attendee). Makes authz meaningful and provides the roster. needs: 08.
- [ ] **10 · Role-based response shaping.** Per-role presenters in `http/response`: an attendee reading an event never receives the roster or any email. Visibility is one half; **scoping is the other** — an attendee reads only events and sessions they are part of, so list results are filtered by membership, not merely stripped of fields. must: (a) a test asserting an attendee's JSON contains no `email` key anywhere; (b) a test asserting an attendee's event and session lists contain only rows they are a member of. needs: 09.
- [ ] **11 · Rooms CRUD.** Rooms belong to an event; standard CRUD behind the chokepoint. needs: 08.
- [ ] **12 · Sessions CRUD.** Sessions belong to an event, reference a room and a speaker, and carry a start/end as `tstzrange`. Correct timezone/DST handling on read and write. Introduce the `Optional[T]` PATCH pattern here (proved rigorously in 20). must: a DST test — a session scheduled across its event's DST boundary returns the correct local start/end and duration on read. needs: 11.
- [ ] **13 · Invitations (single).** Create and list invitations for an event; the base the bulk/idempotent path builds on. needs: 09.
- [ ] **14 · Seed at scale.** Script generating 50 events, 5k users, 50k invitations, with events across multiple time zones and at least one event whose sessions cross a DST boundary. Replaces the `make seed` stub. needs: 12, 13.
- [ ] **15 · Thin read-only agent.** A small CLI loop calling the model with 3–4 read-only tools that hit the public API as a real user (inherits that user's authz). 3 scripted scenarios: a normal read, a permission boundary (attendee), a composition (resolve event + local date + free room). Log each call for legibility. needs: 12, 13.

## Phase 3 — Backend depth (chosen track)

- [ ] **16 · Conflict prevention.** PG18 `EXCLUDE` constraint on `(room_id, tstzrange)` and `(speaker_id, tstzrange)`; map the violation to 409. must: a race test — two goroutines released on a barrier POST conflicting sessions; exactly one 201, one 409; run with `-race`. needs: 12.
- [ ] **17 · Concurrent-edit protection.** `version` column; updates use `WHERE id=$ AND version=$`; 0 rows → 409 with current state (ETag/If-Match or version in body). must: a lost-update test — stale second write gets 409, first writer's data intact. needs: 12.
- [ ] **18 · Query-count independence.** Batch related fetches (event → sessions → rooms/speakers) so query count is constant. must: a query-counting test (pgx tracer) asserting equal counts for 5 vs 500 children. needs: 12, 14.
- [ ] **19 · Keyset pagination.** Cursor over `(created_at, id)` / uuidv7; opaque base64 token; no OFFSET. must: a test paginating deep into the 50k invitations while inserting rows mid-traversal, asserting no skips or duplicates. needs: 14.
- [ ] **20 · PATCH partial-update semantics.** Ensure every update endpoint distinguishes absent vs explicit null via `Optional[T]`; build UPDATEs from set fields only. must: three requests — set / null / omit — produce three distinct outcomes. needs: 12.
- [ ] **21 · Idempotent bulk invite.** `Idempotency-Key` backed by a unique constraint; retries replay stored results; partial failure returns a defined per-item result (207), never a double-send. must: a retry test proving no duplicate sends and stable results. needs: 13.
- [ ] **22 · Audit trail.** Append-only `audit_log` written in the same transaction as each mutation, recording **who** (`actor_id` from the authenticated `sub`), what entity, and before/after — captured with `UPDATE … RETURNING OLD, NEW`. DB role has no UPDATE/DELETE grant on the table. Introduce with the first mutation, complete here. must: a test proving actor, before, and after are all recorded, and that the log can't be mutated. needs: 08.
- [ ] **23 · Go-further pick — recurring sessions.** Store as rules + exceptions with schedule history; materialize on read. Scope small; record what was cut in `TRADEOFFS.md`. (Swap for the caching/two-versions-live or webhooks option only with a note.) needs: 16, 17.

## Phase 4 — Deliverables & verification

Graded artefacts. You author these; they are queued so they can't be forgotten under time pressure.

- [ ] **24 · Three ADRs.** Short. Each: the decision, the alternative rejected, why. Candidates — router choice, agent framework/model, and the go-further pick from 23.
- [ ] **25 · AI-WORKFLOW.md.** Tools used, what was driven vs. delegated, the planning process, and **one specific case where the AI produced wrong code** — how it was caught and what was done. This is the most heavily weighted interview question; draw it from `ISSUE.md` (see the ordering notes). "It worked fine" is not an answer.
- [ ] **26 · TRADEOFFS.md.** Which track and why. What was cut. The decisions Track 1 forced. What two more weeks would buy.
- [ ] **27 · Design diagram (final).** Refresh 01's components and data-model diagrams to match what actually shipped. Legibility matters, polish doesn't. needs: 01.
- [ ] **28 · Fresh-clone verification.** Clone into a clean directory/VM; run `make up && make seed && make test`; confirm it passes in under 5 minutes. Fix anything that isn't reproducible. Run this last, after the docs land.

---

## Notes on ordering

- **PATCH semantics (20) and audit (22) are cross-cutting.** Establish both patterns when the first update/mutation endpoint is built (12 / 08); the depth items are where they're proved rigorously across all endpoints.
- **Seed at scale (14) gates the scale/pagination proofs (18, 19).** Don't attempt those tests against a stub seed.
- **Capture AI mistakes the day they happen.** When AI-generated code is wrong, log it to `ISSUE.md` immediately — symptom, root cause, how it was caught, the fix — and add the one-line rule to `FAILURES.md`. Item 25 is graded on one specific, concrete incident, and that account cannot be reconstructed weeks later.
- **CI exists from item 03.** Every later item that adds a proof adds it to the CI workflow too; the brief asks CI, not a local run, to verify the contract.
- **If time runs short,** cut 23 (go-further) and trim the agent (15) before cutting any `must:` test. Correct semantics at 70% scope beats full scope with a race. Record every cut in `TRADEOFFS.md`.
