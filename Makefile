# Event Management Platform.
#
# THE GRADED CONTRACT is `make up && make seed && make test` on a clean machine
# with Docker, under five minutes. Which forces one rule:
#
#     `make up` is IDEMPOTENT. `make down` is the destructive one.
#
# If `up` wiped volumes, the chain above would destroy what `seed` just wrote
# before `test` ever saw it -- and at item 14 that is 50k invitations vanishing
# silently. Re-running `up` must always be safe.

SHELL := /bin/bash

# .env (gitignored) overrides; the ?= defaults below then fill in the rest, so a
# clean clone with no .env at all still works. Same values as .env.example.
-include .env

ENV                ?= development
POSTGRES_USER      ?= krane_migrator
POSTGRES_PASSWORD  ?= dev_only_migrator
POSTGRES_DB        ?= krane
POSTGRES_PORT      ?= 5432
POSTGRES_TEST_DB   ?= krane_test
# cmd/seed's own tests need full-table DELETEs (Truncate) to prove the real
# thing, which would race every other package sharing POSTGRES_TEST_DB (see
# CLAUDE.md's "Isolation is per-package, never per-test" / FAILURES.md's
# keyset-tie-break note on shared-database assumptions) -- so it gets its
# own database, per CLAUDE.md's own anticipated escape hatch: "CREATE
# DATABASE <pkg> TEMPLATE krane_test".
POSTGRES_SEED_TEST_DB ?= cmd_seed
KRANE_APP_USER     ?= krane_app
KRANE_APP_PASSWORD ?= dev_only_app
OIDC_PORT          ?= 9090
OIDC_AUDIENCE      ?= krane-api
API_PORT           ?= 8080

export

# The committed dev defaults, named so the production guard can recognise them.
DEV_POSTGRES_PASSWORD  := dev_only_migrator
DEV_KRANE_APP_PASSWORD := dev_only_app

# Host-facing DSNs, as the runtime role. The API and the tests are clients, never
# the migrator.
DATABASE_URL      ?= postgres://$(KRANE_APP_USER):$(KRANE_APP_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
TEST_DATABASE_URL ?= postgres://$(KRANE_APP_USER):$(KRANE_APP_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_TEST_DB)?sslmode=disable

# Host-facing DSNs, as krane_migrator -- cmd/seed connects as the migrator,
# not krane_app: seeding is an offline, admin-style data-load (truncate +
# bulk load), and krane_app deliberately has no DELETE/TRUNCATE grant on
# audit_log (item 02, append-only) that a re-seed after real API activity
# needs (FEATURES.md item 14). TEST_SEED_DATABASE_URL is the same role
# against krane_test, for cmd/seed's own integration tests.
SEED_DATABASE_URL      ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
TEST_SEED_DATABASE_URL ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_SEED_TEST_DB)?sslmode=disable

# Migration DSNs run inside the compose network, as krane_migrator.
MIGRATE_URL_DEV  := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_DB)?sslmode=disable
MIGRATE_URL_TEST := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_TEST_DB)?sslmode=disable

# In-network equivalents of the host-facing DSNs/URLs above, for `gotools`
# (seed/test run inside the compose network, so "localhost" would not
# resolve to postgres/oidc -- same reasoning as MIGRATE_URL_DEV/TEST).
SEED_DATABASE_URL_INNET      := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_DB)?sslmode=disable
TEST_DATABASE_URL_INNET      := postgres://$(KRANE_APP_USER):$(KRANE_APP_PASSWORD)@postgres:5432/$(POSTGRES_TEST_DB)?sslmode=disable
TEST_SEED_DATABASE_URL_INNET := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_SEED_TEST_DB)?sslmode=disable
OIDC_ISSUER_URL_INNET        := http://oidc:8080/default

# Pinned so local and CI run byte-identical versions; lint cannot pass here and
# fail there.
GOLANGCI_LINT_IMAGE := golangci/golangci-lint:v1.62.2-alpine

OIDC_DISCOVERY   := http://localhost:$(OIDC_PORT)/default/.well-known/openid-configuration
OIDC_ISSUER_URL  := http://localhost:$(OIDC_PORT)/default

.DEFAULT_GOAL := help
.PHONY: help up down seed test test-verbose test-db-reset lint generate contract-check migrate-up migrate-down psql guard-production-credentials wait-oidc token

help: ## Show the available targets
	@echo "Event Management Platform"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  make up is idempotent. make down is the destructive one."

## ---------------------------------------------------------------------------
## The four graded targets
## ---------------------------------------------------------------------------

up: guard-production-credentials ## Start Postgres + mock OIDC and migrate (idempotent)
	docker compose up -d --wait postgres oidc
	@$(MAKE) --no-print-directory wait-oidc
	docker compose run --rm migrate -database "$(MIGRATE_URL_DEV)" up
	docker compose run --rm app-role
	@echo "up: ready. api $(DATABASE_URL) | oidc $(OIDC_DISCOVERY)"

seed: up ## Load 50 events / 5k users / 50k invitations (idempotent -- safe to re-run)
	docker compose run --rm \
		-e SEED_DATABASE_URL="$(SEED_DATABASE_URL_INNET)" \
		-e OIDC_ISSUER_URL="$(OIDC_ISSUER_URL_INNET)" \
		gotools go run ./cmd/seed

test: test-db-reset ## Run the suite against a freshly migrated throwaway database
	docker compose run --rm \
		-e TEST_DATABASE_URL="$(TEST_DATABASE_URL_INNET)" \
		-e TEST_SEED_DATABASE_URL="$(TEST_SEED_DATABASE_URL_INNET)" \
		-e OIDC_ISSUER_URL="$(OIDC_ISSUER_URL_INNET)" \
		gotools go test ./... -race -count=1

test-verbose: test-db-reset ## Like test, but with go test -v -- local debugging only, not the graded path
	docker compose run --rm \
		-e TEST_DATABASE_URL="$(TEST_DATABASE_URL_INNET)" \
		-e TEST_SEED_DATABASE_URL="$(TEST_SEED_DATABASE_URL_INNET)" \
		-e OIDC_ISSUER_URL="$(OIDC_ISSUER_URL_INNET)" \
		gotools go test ./... -race -count=1 -v

test-db-reset: up
	@echo "test: recreating $(POSTGRES_TEST_DB)"
	@docker compose exec -T -e PGPASSWORD="$(POSTGRES_PASSWORD)" postgres \
		psql -v ON_ERROR_STOP=1 -U "$(POSTGRES_USER)" -d postgres --quiet \
			-c 'DROP DATABASE IF EXISTS "$(POSTGRES_TEST_DB)" WITH (FORCE)' \
			-c 'CREATE DATABASE "$(POSTGRES_TEST_DB)"'
	docker compose run --rm migrate -database "$(MIGRATE_URL_TEST)" up
	@echo "test: recreating $(POSTGRES_SEED_TEST_DB) (cmd/seed's own database -- see the comment on POSTGRES_SEED_TEST_DB)"
	@docker compose exec -T -e PGPASSWORD="$(POSTGRES_PASSWORD)" postgres \
		psql -v ON_ERROR_STOP=1 -U "$(POSTGRES_USER)" -d postgres --quiet \
			-c 'DROP DATABASE IF EXISTS "$(POSTGRES_SEED_TEST_DB)" WITH (FORCE)' \
			-c 'CREATE DATABASE "$(POSTGRES_SEED_TEST_DB)" TEMPLATE "$(POSTGRES_TEST_DB)"'

lint: ## gofmt + go vet + golangci-lint (pinned image, no host install)
	@./scripts/require-go.sh
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: these files are not formatted:" >&2; \
		echo "$$unformatted" >&2; \
		echo "Run: gofmt -w ." >&2; \
		exit 1; \
	fi
	go vet ./...
	docker run --rm \
		-v "$(CURDIR):/app" \
		-v krane-golangci-cache:/root/.cache \
		-v krane-gomod-cache:/go/pkg/mod \
		-w /app $(GOLANGCI_LINT_IMAGE) golangci-lint run

## ---------------------------------------------------------------------------
## OpenAPI contract (item 05)
## ---------------------------------------------------------------------------

generate: ## Regenerate internal/http/gen from openapi/openapi.yaml
	@./scripts/require-go.sh
	go generate ./...

contract-check: generate ## Fail if the spec or generated code has drifted
	@if ! git diff --exit-code -- internal/http/gen openapi >/dev/null; then \
		echo "contract-check: internal/http/gen or openapi/ changed after regenerating." >&2; \
		echo "The committed spec/generated code is out of date -- run 'make generate' and commit the result." >&2; \
		git diff -- internal/http/gen openapi >&2; \
		exit 1; \
	fi

## ---------------------------------------------------------------------------
## Helpers
## ---------------------------------------------------------------------------

down: ## Stop everything and DELETE all data (the only destructive target)
	docker compose --profile tools down -v --remove-orphans

migrate-up: ## Apply migrations to the dev database
	docker compose run --rm migrate -database "$(MIGRATE_URL_DEV)" up

migrate-down: ## Roll the dev database back one migration
	docker compose run --rm migrate -database "$(MIGRATE_URL_DEV)" down 1

psql: ## Open a psql shell on the dev database as the migrator
	docker compose exec -e PGPASSWORD="$(POSTGRES_PASSWORD)" postgres \
		psql -U "$(POSTGRES_USER)" -d "$(POSTGRES_DB)"

# USER names a demo identity for convenience -- it grants no privilege by
# itself. Roles are per-event and live only in event_members (item 09+); a
# token never encodes one.
token: ## Mint a demo JWT via the mock OIDC issuer: make token USER=admin|contributor|attendee
	@case "$(USER)" in \
		admin|contributor|attendee) ;; \
		*) echo "usage: make token USER=admin|contributor|attendee" >&2; exit 1 ;; \
	esac
	@curl -sf -X POST "http://localhost:$(OIDC_PORT)/default/token" \
		-d grant_type=client_credentials -d client_id=demo-$(USER) -d client_secret=unused \
		| grep -o '"access_token"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -E 's/.*"([^"]+)"$$/\1/'

# The mock OIDC image ships without a shell, so it cannot carry a compose
# healthcheck. Poll its discovery document from the host instead.
wait-oidc:
	@for i in $$(seq 1 60); do \
		if curl -fsS "$(OIDC_DISCOVERY)" >/dev/null 2>&1; then exit 0; fi; \
		sleep 1; \
	done; \
	echo "oidc: no discovery document at $(OIDC_DISCOVERY) after 60s" >&2; \
	exit 1

# A comment nobody reads is not a control. If someone points this at production
# while the committed dev passwords are still in place, refuse to start.
guard-production-credentials:
	@if [ "$(ENV)" = "production" ] || [ "$${APP_ENV:-}" = "production" ]; then \
		failed=0; \
		if [ "$(POSTGRES_PASSWORD)" = "$(DEV_POSTGRES_PASSWORD)" ]; then \
			echo "REFUSING TO START: POSTGRES_PASSWORD is still the committed dev default." >&2; \
			failed=1; \
		fi; \
		if [ "$(KRANE_APP_PASSWORD)" = "$(DEV_KRANE_APP_PASSWORD)" ]; then \
			echo "REFUSING TO START: KRANE_APP_PASSWORD is still the committed dev default." >&2; \
			failed=1; \
		fi; \
		if [ "$$failed" = "1" ]; then \
			echo "" >&2; \
			echo "ENV=production, but these values are published in .env.example and in git" >&2; \
			echo "history. Set real secrets in the environment before starting." >&2; \
			exit 1; \
		fi; \
	fi
