# AUDIT.md

Append-only activity log. One timestamped entry per completed feature or fix, newest last. Format:

```
## YYYY-MM-DD HH:MM — <feature or fix name>
<what was done, in a sentence or two>
```

---

## 2026-08-23 20:17 — Feature 00 · git-ai + repo skeleton

Completed feature 00. Added `go.mod` (module `github.com/Uzama/krane-event-management-platform`, Go 1.23.0); the rest of the skeleton — `git init`, `.gitignore`, `CLAUDE.md`, `FAILURES.md` — already existed, and git-ai capture was verified live rather than set up fresh.

Bundled the preceding `/init` review of the instruction files: trimmed `FAILURES.md` from 36 lines to 7 (14 of its 16 rules restated `CLAUDE.md` invariants, so both files were charging every session twice for the same rules), folding the two net-new rules into `CLAUDE.md`; pinned Go 1.23; documented the single-test command; added a pre-code note. Created `AUDIT.md` and `ISSUE.md`.

Audited `FEATURES.md` against the assignment brief and applied 8 amendments closing 6 gaps and 2 inconsistencies — CI had no owner, the diagram covered only the data model, the contributor and attendee role boundaries were unencoded and untested, DST had no test, audit recorded no actor, and item 18's dependency contradicted the file's own ordering notes. Added Phase 4 to track the four graded deliverables. Queue is now 29 items.

Commit `5e9bf10`.

## 2026-08-23 21:47 — Feature 01 · Requirement analysis + design diagram

Completed feature 01. Produced both halves of the required design diagram plus the written analysis behind them: `docs/requirements.md` (nine entities with full column tables, relationships, the per-event role matrix expressed as the `role_permissions` rows item 07 will seed, an invariant→column map, eight recorded decisions, and the remaining open question), `docs/er-diagram.md` (Mermaid `erDiagram`, constraint table, legend), and `docs/components.md` (layers, a request traced end to end through the middleware chain and back out the error path, and the compose topology). Moved the assignment brief from `doc/` to `docs/` so there is one doc directory.

Eight decisions recorded so items 02–23 don't re-litigate them. Two are scope cuts made deliberately and logged for `TRADEOFFS.md`: the invitation acceptance lifecycle (invitations stay an independent record; membership is populated directly, keeping every read path behind the per-event authz chokepoint), and cross-event physical-room conflicts (rooms are per-event, so item 16's `EXCLUDE` covers exactly the conflicts the model claims to have — the known limitation is that two events in the same building can both book Hall A). Both carry a "what two more weeks would buy" line.

The remaining open question — whether the email-visibility ban extends below attendee to contributors — has a drafted but unsent email at `docs/requirements.md` §7.3. It does not block item 02 and does not weaken the item 10 `must:` test, which asserts the attendee case the brief names.

Verification: all four Mermaid blocks parse under mermaid@11 driven by a jsdom DOM. Bare Node fails *every* flowchart with a missing-DOM error regardless of content, so the checker asserts a trivial flowchart parses first — without that self-check the run would have reported false failures. Scripted checks confirmed all nine tables item 02 names appear in both the ER diagram and the analysis, and that no invitation `status`/`responded_at` column survives anywhere.

No code, and no TDD step: this feature produces documents, and `make test` does not exist until item 03. Flagged at the review gate rather than skipped silently.


## 2026-08-23 22:40 — Feature 02 · DDL / schema migrations

Completed feature 02. One timestamped golang-migrate pair, `migrations/20260823164421_init_schema.{up,down}.sql`, carrying all nine tables from `docs/requirements.md` §2 — `users`, `events`, `event_members`, `role_permissions`, `rooms`, `sessions`, `invitations`, `audit_log`, `idempotency_keys` — with `uuidv7()` PKs, `timestamptz` throughout, `events.timezone` holding an IANA name, and session time as a single `tstzrange`.

`version` and `deleted_at` columns ship now; both partial `EXCLUDE` constraints are deferred to item 16, with their exact DDL sitting in a comment on `sessions`, so 16's race test can fail for the right reason before it passes. `role_permissions` is created empty — item 07 seeds it.

Introduced the two-role model the rest of the project depends on. `krane_migrator` owns the database and every object the migrations create; `krane_app` is the API's runtime identity. **Item 03 must set `POSTGRES_USER=krane_migrator` in compose and point the API DSN at `krane_app`** — `ALTER DEFAULT PRIVILEGES` says `FOR ROLE krane_migrator` explicitly, and if migrations ever run as a different role every future table reaches the API with no grants. `krane_app` is created with `LOGIN` and no password; item 03 sets it from the environment rather than committing a credential to a migration.

Two tables are special-cased away from the blanket DML grant, each with its own grant and its own unconditional `REVOKE` as the last statements of the file: `audit_log` is `INSERT, SELECT` only (append-only is a grant, not a convention), and `role_permissions` is `SELECT` only — the API reads the policy that governs it and cannot rewrite its own permission rules. Both revokes are order-independent by design, so a later reordering or a future blanket grant cannot silently widen them.

Three decisions were recorded in the docs rather than left implicit: **D9** — soft-deleting a session releases its room and speaker slot immediately, stated as a product rule in `docs/requirements.md` §7.3 (new) and referenced from the `deleted_at` legend line in `docs/er-diagram.md`, because item 16's partial `EXCLUDE` makes it true whether or not anyone intended it; **D10** — `event_members.role` and `invitations.role` are `CHECK`-constrained while `role_permissions.role` deliberately is not, since that table is the registry (for item 24's ADR); and the half-open `[)` range convention, documented as a column comment on `sessions.time_range` so items 12 and 16 build back-to-back sessions that do not falsely conflict.

Verification, in place of the `make test` that does not exist until item 03: fourteen gates against a throwaway `postgres:18` (18.6) container started with `POSTGRES_USER=krane_migrator`, then torn down. Two gates ran *before* any DDL was written — `uuidv7()`'s exact signature was confirmed from `pg_catalog` rather than assumed (zero-arg form; an `interval`-shifted overload also exists), and extension ordering was proven in both directions (a `citext` column without the extension errors; dropping `citext` while such a column exists errors, and `CASCADE` would take the column with it). The rest: nine tables present, zero naive timestamp columns, `tstzrange`/`citext` where the design says, v7 version nibble on generated ids, all six unique constraints, the `role_permissions` composite PK, the role `CHECK` rejecting `'organiser'` and accepting `'contributor'`, the audit and policy privilege matrices, live `permission denied` on `UPDATE`/`DELETE`/`TRUNCATE` of both protected tables while connected **as `krane_app`**, and `[)` back-to-back ranges proven not to overlap where `[]` would.

The `ON SEQUENCES` default grant was dropped after confirming the schema owns no sequences — zero sequences, zero identity columns, zero `serial` columns — but its `REVOKE` is kept in the down file as a deliberate no-op so a future sequence grant cannot strand `DROP ROLE`. That the default-privileges revokes are load-bearing was proven, not assumed: stripping them makes the down migration fail at `DROP ROLE` with a non-zero exit rather than continuing. Full `up → down → up` round trip is clean — down leaves zero tables, zero roles, zero extensions and zero default-ACL entries, and the second up restores all nine tables with the privilege matrix intact.

Gate 10 exercises FK cascade on a hard `DELETE FROM events`. It is a safety net only: events are soft-deleted, so that path does not run in normal operation and it is not evidence that deleting an event is handled — that is items 08 and 22.

No `FAILURES.md` rule added: nothing went wrong that isn't already an invariant in `CLAUDE.md`.
