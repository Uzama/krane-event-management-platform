# Krane — Event Management Platform

A Go REST/JSON API over PostgreSQL 18 that schedules **sessions** inside **events**, in **rooms**, with people who hold per-event **roles** (admin / contributor / attendee). It includes a thin, read-only CLI **agent** that talks to the API as a real authenticated user.

Built as a clean-architecture backend: `domain` (business rules, no framework imports) ← `adapter` (Postgres, JWT auth, authorization data) and `http` (routing, handlers, request/response DTOs), wired together by `container`/`bootstrap`. See [`CLAUDE.md`](./CLAUDE.md) for the full layout, and [`docs/`](./docs) for the design diagrams and requirement analysis.

Track chosen: **Backend depth** (data correctness under load) — see [`TRADEOFFS.md`](./TRADEOFFS.md) for what that meant and what it cost.

---

## Working setup

```bash
make up && make seed && make test
```

No other setup is required. `.env.example` documents every value the commands above use; none of it needs to be copied to `.env` for the default flow — the `Makefile` falls back to the same values as inline defaults, so a bare clone works with zero configuration.

### Prerequisites

- Docker + Docker Compose

That's it — `make up`, `make seed`, and `make test` all run entirely in containers (Postgres, the mock OIDC issuer, migrations, and now `go run`/`go test` too, via a pinned `golang:1.23.12-alpine` toolchain container). No host Go install is required for the graded path. Go 1.23+ on the host is only needed for `make lint`/`make generate`/`make contract-check`, which aren't part of `make up && make seed && make test`.

### `make up` — start Postgres + mock OIDC, migrated

```bash
make up
```

Starts PostgreSQL 18 (pinned) and a mock OIDC issuer via Docker Compose, waits for both to be ready, and runs migrations. **Idempotent** — safe to run again at any time; it never touches existing data. `make seed` and `make test` both depend on it, so you rarely need to call it directly.

### `make seed` — load demo data

```bash
make seed
```

Generates 50 events, 5,000 users, and 50,000 invitations directly into the dev database (bypassing the API, via `pgx.CopyFrom`), spanning multiple time zones with at least one event whose sessions cross a DST boundary. Prints three demo bearer tokens (admin / contributor / attendee) you can use against the running API. Also idempotent — re-seeding clears and reloads only the data `cmd/seed` itself owns.

Mint an individual token any time with:

```bash
make token USER=admin        # or contributor / attendee
```

### `make test` — run the suite

```bash
make test
```

Recreates a throwaway `krane_test` database, migrates it fresh, and runs the full Go test suite (`go test ./... -race -count=1`) inside the same toolchain container, against real Postgres over the compose network — no mocks for repository or integration tests. This is what CI runs on every push, alongside `make lint`. Use `make test-verbose` (adds `-v`) for local debugging; it's not part of the graded path.

### `make lint` — static checks

```bash
make lint
```

`gofmt`, `go vet`, and `golangci-lint` (pinned image, no host install required).

### `make down` — stop everything

```bash
make down
```

Stops all containers and **deletes all data** (volumes included). This is the only destructive target — `up`, `seed`, and `test` are all safe to re-run without it.

### Other useful targets

| Target | What it does |
|---|---|
| `make psql` | Open a `psql` shell on the dev database |
| `make migrate-up` / `make migrate-down` | Apply / roll back one migration on the dev database |
| `make generate` | Regenerate `internal/http/gen` from `openapi/openapi.yaml` |
| `make contract-check` | Fail if the OpenAPI spec or generated code has drifted (run in CI) |
| `make help` | List every target with a one-line description |

---

## Running the agent

Once `make up` and `make seed` have run, the thin read-only CLI agent (`cmd/agent`) can query the live API as an authenticated user:

```bash
export ANTHROPIC_API_KEY=...   # required — the agent calls the Messages API directly
export KRANE_TOKEN=$(make token USER=admin)
go run ./cmd/agent -scenario=1   # a normal read
go run ./cmd/agent -scenario=2   # a permission boundary (attendee hits a 403)
go run ./cmd/agent -scenario=3   # a composition: event + local date + free room
```

The agent never mints or elevates credentials — it inherits whatever role the bearer token it's given holds, exactly like any other API client. No live Anthropic call happens during `make test`; the tool-call loop is proven against a scripted fake model client there.

---

## Repository map

```
cmd/api      entrypoint — calls bootstrap.Boot()
cmd/agent    thin read-only CLI agent (an API client, not part of the layered core)
cmd/seed     seed generator — 50 events / 5k users / 50k invitations
internal/    domain, adapter, http, utils, container, bootstrap — see CLAUDE.md
migrations/  timestamped, append-only .sql
openapi/     the API contract; validated against the running server in tests
docs/        requirement analysis, ER diagram, components diagram
```

For the architectural decisions behind the code — why this router, why this agent client, why recurring sessions were shaped the way they were — see [`docs/adr/`](./docs/adr). For what got cut and why, see [`TRADEOFFS.md`](./TRADEOFFS.md). For how AI was used to build this and one place it produced wrong code, see [`AI-WORKFLOW.md`](./AI-WORKFLOW.md).
