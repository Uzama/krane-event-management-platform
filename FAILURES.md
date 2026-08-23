# FAILURES.md

An imperative log of mistakes and repo gotchas. Every line exists because something went wrong at least once. Read it before starting any task. Add a line — in the imperative, describing the correct behaviour — whenever a mistake reveals a rule that wasn't written down, in the same commit as the fix.

This file is imported into every session from `CLAUDE.md`. Keep entries one line each and specific to this repo.

## Concurrency and data correctness

- Never generate a check-then-insert for conflicts; two of them race. Use the `EXCLUDE` constraint and translate the violation to 409.
- Never model a nullable PATCH field as `*string`; it cannot tell an absent field from an explicit null. Use `Optional[T]`.
- Never use `OFFSET` for pagination; it drifts when rows are inserted mid-traversal and slows down on deep pages. Keyset only.
- Never write a "concurrency" test that calls the endpoint sequentially. Synchronize goroutines on a barrier and release them together, and run with `-race`.
- Never fetch related rows in a loop; that is N+1. Batch with `WHERE id = ANY($1)` or JOIN, and keep query count constant.
- Never capture audit "before" with a separate SELECT then UPDATE; another writer can slip between. Use `UPDATE ... RETURNING OLD, NEW`.

## Auth and authorization

- Never issue or sign a JWT inside the API. Validate only, via JWKS.
- Never put a role check inside a handler. Route it through the authz chokepoint and the `role_permissions` table.
- Never return the full object shape regardless of role. Field visibility is role-driven; an attendee must not receive rosters or emails.

## Testing and workflow

- Never mock the database in repo or integration tests. Run against real Postgres in Docker.
- Never mark a task done on a red loop, and never `t.Skip` or comment out a test to reach green.
- Never run `git commit` before the human confirms the message.

## Architecture

- Never put the container (wiring) in `utils`. `utils` is leaf; `http` imports it and the container imports `http` — same package means an import cycle. Container is its own outermost package.
- Never import `adapter` from `http` or `http` from `adapter`. They meet only in `container`.
- Never import a framework into `domain` (no pgx, net/http, goqu). If a domain file needs one, the logic is in the wrong layer.

## Time

- Never store naive timestamps or fixed UTC offsets. Use `timestamptz` and store the event's IANA timezone name.
