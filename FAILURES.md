# FAILURES.md

Imperative one-liners, each written because something actually went wrong. Loaded into every session from `CLAUDE.md`, so add a rule only when it isn't already an invariant there — see "Keeping context current".

## Rules

- Mount the Postgres volume at `/var/lib/postgresql`, never `/var/lib/postgresql/data` — the `postgres:18+` images keep data in a major-version subdirectory and refuse to start on the old path.
- Before pinning any dependency, check its own `go` directive against ours; a module requiring a newer Go than `go.mod` declares fails to resolve rather than degrading gracefully.
- Escape a literal `${...}` in docker-compose.yml as `$${...}` whenever it's meant for the container's own env-var templating, not Compose's — Compose interpolates `${VAR}` in every string value (including multi-line ones) before the container ever sees it, silently defaulting undefined names to blank.
- When testing keyset pagination's tie-break, force ≥2 rows onto an identical `created_at` (e.g. a direct `UPDATE`) — distinct timestamps let a broken `created_at`-only `ORDER BY`/cursor `WHERE` pass anyway, since the tie never gets exercised.
