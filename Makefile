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
KRANE_APP_USER     ?= krane_app
KRANE_APP_PASSWORD ?= dev_only_app
OIDC_PORT          ?= 9090

export

# The committed dev defaults, named so the production guard can recognise them.
DEV_POSTGRES_PASSWORD  := dev_only_migrator
DEV_KRANE_APP_PASSWORD := dev_only_app

# Host-facing DSNs, as the runtime role. The API and the tests are clients, never
# the migrator.
DATABASE_URL      ?= postgres://$(KRANE_APP_USER):$(KRANE_APP_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
TEST_DATABASE_URL ?= postgres://$(KRANE_APP_USER):$(KRANE_APP_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_TEST_DB)?sslmode=disable

# Migration DSNs run inside the compose network, as krane_migrator.
MIGRATE_URL_DEV  := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_DB)?sslmode=disable
MIGRATE_URL_TEST := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_TEST_DB)?sslmode=disable

# Pinned so local and CI run byte-identical versions; lint cannot pass here and
# fail there.
GOLANGCI_LINT_IMAGE := golangci/golangci-lint:v1.62.2-alpine

OIDC_DISCOVERY := http://localhost:$(OIDC_PORT)/default/.well-known/openid-configuration

.DEFAULT_GOAL := help
.PHONY: help up down seed test lint migrate-up migrate-down psql guard-production-credentials wait-oidc

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

seed: ## Load demo data (stub until item 14)
	@echo "seed: stub. Real seed data -- 50 events / 5k users / 50k invitations,"
	@echo "      across time zones and crossing a DST boundary -- is item 14 in FEATURES.md."

test: up ## Run the suite against a freshly migrated throwaway database
	@./scripts/require-go.sh
	@echo "test: recreating $(POSTGRES_TEST_DB)"
	@docker compose exec -T -e PGPASSWORD="$(POSTGRES_PASSWORD)" postgres \
		psql -v ON_ERROR_STOP=1 -U "$(POSTGRES_USER)" -d postgres --quiet \
			-c 'DROP DATABASE IF EXISTS "$(POSTGRES_TEST_DB)" WITH (FORCE)' \
			-c 'CREATE DATABASE "$(POSTGRES_TEST_DB)"'
	docker compose run --rm migrate -database "$(MIGRATE_URL_TEST)" up
	go test ./... -race -count=1

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
