# FAILURES.md

Imperative one-liners, each written because something actually went wrong. Loaded into every session from `CLAUDE.md`, so add a rule only when it isn't already an invariant there — see "Keeping context current".

## Rules

- Mount the Postgres volume at `/var/lib/postgresql`, never `/var/lib/postgresql/data` — the `postgres:18+` images keep data in a major-version subdirectory and refuse to start on the old path.
- Before pinning any dependency, check its own `go` directive against ours; a module requiring a newer Go than `go.mod` declares fails to resolve rather than degrading gracefully.
