# nodate-flow Makefile
# Entry points for local development, testing, linting, and code generation.
# Run `make help` to list available targets.

SHELL := /bin/bash

# Auto-load .env if present so `make dev` etc. pick up NF_* variables
# without the user having to `source .env` manually. A missing file is
# not an error (first-run case).
ifneq (,$(wildcard ./.env))
include .env
export
endif

# Package manager — bun only. Do not override.
PKG := bun
PKG_RUN := bun run
PKG_X := bunx

.DEFAULT_GOAL := help

# ---------- meta ----------

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---------- dev (yarn dev equivalent) ----------

.PHONY: dev dev-api dev-auth-api dev-worker dev-presence dev-web dev-accounts-web dev-reset reload stop-dev up down logs
dev: db-schema .env ## Start MySQL (compose) + auth API + flow API + accounts web + flow web
	@echo "starting mysql, auth-api, flow-api, accounts-web, flow-web..."
	@docker compose up -d mysql
	@$(MAKE) -j4 dev-auth-api dev-api dev-accounts-web dev-web

stop-dev: ## Kill any running dev servers (auth-api + flow-api + accounts-web + flow-web), idempotent
	@echo "stopping dev servers..."
	@# Kill the make dev-* wrappers so they don't respawn their Go/Vite children
	@pkill -f 'make dev-api$$|make dev-auth-api$$|make dev-web$$|make dev-accounts-web$$' 2>/dev/null || true
	@# Belt-and-braces: close anything still bound to the standard ports
	@for port in 8080 8082 9090 5173 5175; do \
	  pids="$$(lsof -nP -iTCP:$$port -sTCP:LISTEN -t 2>/dev/null)"; \
	  if [ -n "$$pids" ]; then \
	    echo "  :$$port -> $$pids"; \
	    kill $$pids 2>/dev/null || true; \
	  fi; \
	done
	@sleep 1

reload: stop-dev dev ## Stop running dev servers and start `make dev` fresh

dev-reset: ## Full reset: nuke volumes, rebuild schema, start fresh, seed
	@echo "resetting everything from scratch..."
	docker compose down -v || true
	@$(MAKE) db-schema
	docker compose up -d mysql
	@echo "waiting for mysql to be healthy (schema init may take a moment)..."
	@until docker compose exec mysql mysql -u root -prootpw -e "SELECT 1 FROM workspaces LIMIT 0" nodate_flow 2>/dev/null; do sleep 2; done
	@echo "mysql ready, seeding..."
	@$(MAKE) seed-flow
	@$(MAKE) dev

# Copy .env.example on first run so `make dev` works out of the box.
.env:
	@echo "creating .env from .env.example"
	cp .env.example .env
	@# Default DSN in .env.example targets the compose "mysql" host, but
	@# `make dev` runs the api on the host so point it at 127.0.0.1.
	@sed -i.bak 's|@tcp(mysql:3306)|@tcp(127.0.0.1:3306)|' .env && rm -f .env.bak

dev-api: ## Run Go flow API against the local MySQL (reads .env)
	cd apps/flow-api && \
	  NF_DB_DSN="$${NF_DB_DSN:-$(NF_DB_USER):$(NF_DB_PASSWORD)@tcp($(NF_DB_HOST):$(NF_DB_PORT))/$(NF_DB_NAME)?parseTime=true&charset=utf8mb4&collation=utf8mb4_0900_ai_ci}" \
	  go run ./cmd/api

dev-auth-api: ## Run Go auth API against the local MySQL (reads .env)
	cd apps/auth-api && \
	  NF_DB_DSN="$${NF_DB_DSN:-$(NF_DB_USER):$(NF_DB_PASSWORD)@tcp($(NF_DB_HOST):$(NF_DB_PORT))/$(NF_DB_NAME)?parseTime=true&charset=utf8mb4&collation=utf8mb4_0900_ai_ci}" \
	  go run ./cmd/api

dev-worker: db-schema .env ## Run Go flow-worker natively against the local MySQL (not started by `make dev`)
	cd apps/flow-worker && \
	  NF_DB_DSN="$${NF_DB_DSN:-$(NF_DB_USER):$(NF_DB_PASSWORD)@tcp($(NF_DB_HOST):$(NF_DB_PORT))/$(NF_DB_NAME)?parseTime=true&charset=utf8mb4&collation=utf8mb4_0900_ai_ci}" \
	  NF_FLOW_WORKER_LOG_LEVEL=debug \
	  go run ./cmd/worker

dev-presence: .env ## Run Go presence-discord gateway natively (not started by `make dev`)
	cd apps/presence-discord && \
	  NF_PRESENCE_LOG_LEVEL=debug \
	  go run ./cmd/gateway

dev-web: ## Run Vite dev server (flow-web, port 5173)
	cd apps/flow-web && $(PKG_RUN) dev

dev-accounts-web: ## Run Vite dev server (accounts-web, port 5175)
	cd apps/accounts-web && $(PKG_RUN) dev

up: ## docker compose up -d (full stack)
	docker compose up -d

down: ## docker compose down
	docker compose down

logs: ## Tail compose logs
	docker compose logs -f

# ---------- build ----------

.PHONY: build build-api build-auth-api build-worker build-presence build-web build-accounts-web
build: build-api build-auth-api build-worker build-presence build-web build-accounts-web ## Build all apps

build-api:
	cd apps/flow-api && go build -o ../../bin/flow-api ./cmd/api

build-auth-api:
	cd apps/auth-api && go build -o ../../bin/auth-api ./cmd/api

build-worker:
	cd apps/flow-worker && go build -o ../../bin/flow-worker ./cmd/worker

build-presence:
	cd apps/presence-discord && go build -o ../../bin/presence-discord ./cmd/gateway

build-web:
	cd apps/flow-web && $(PKG_RUN) build

build-accounts-web:
	cd apps/accounts-web && $(PKG_RUN) build

# ---------- test ----------

.PHONY: test test-api test-api-mock test-api-real test-web test-accounts-web test-ui test-sdk test-e2e test-contract test-openapi-diff test-schema-collisions test-schema-diff verify-codegen test-core-contract lighthouse
test: test-api test-auth-api test-web test-accounts-web test-ui test-sdk ## Run unit/integration tests (Go + TS)

test-api: test-api-mock test-api-real ## Go tests (flow) — both NF_FLOW_AI_MOCK on and off

test-api-mock: ## Go tests (flow) with NF_FLOW_AI_MOCK=1 (mock AI orchestrator path)
	cd apps/flow-api && NF_FLOW_AI_MOCK=1 go test ./...

test-api-real: ## Go tests (flow) with NF_FLOW_AI_MOCK unset (real per-tenant provider path)
	cd apps/flow-api && env -u NF_FLOW_AI_MOCK go test ./...

test-auth-api: ## Go tests (auth)
	cd apps/auth-api && go test ./...

test-web: ## Vitest (flow-web)
	cd apps/flow-web && $(PKG_RUN) test

test-accounts-web: ## Vitest (accounts-web)
	cd apps/accounts-web && $(PKG_RUN) test

test-ui: ## Vitest (packages/ui)
	cd packages/ui && $(PKG_RUN) test

test-sdk: ## Vitest (packages/sdk)
	cd packages/sdk && $(PKG_RUN) test

test-e2e: ## Playwright E2E
	cd apps/flow-web && $(PKG_RUN) e2e

test-contract: ## Schemathesis contract tests (requires running API)
	./scripts/contract-test.sh

test-openapi-diff: ## Fail if the committed OpenAPI specs drift from the live Go sources
	./scripts/openapi-diff.sh

test-schema-collisions: ## Fail if the merged OpenAPI spec has schema name collisions
	./scripts/check-schema-collisions.sh

test-schema-diff: ## Fail if sql/schema.sql is out of sync with sql/core/** and sql/flow/**
	./scripts/schema-diff.sh

verify-codegen: ## Fail if sql/schema.sql or the sqlc output is out of sync with the schema
	bash scripts/check-codegen-drift.sh

test-core-contract: ## Check a database against the core contract (NF_CONFORMANCE_DSN or NF_DB_* vars)
	bash sql/core/conformance/run.sh \
	  --dsn "$${NF_CONFORMANCE_DSN:-$(NF_DB_USER):$(NF_DB_PASSWORD)@$(NF_DB_HOST):$(NF_DB_PORT)/$(NF_DB_NAME)}" \
	  --mode $${NF_CONFORMANCE_MODE:-schema}

lighthouse: build-web ## Run Lighthouse CI (a11y 95+, perf 70+)
	$(PKG_X) @lhci/cli autorun

# ---------- lint / format / typecheck ----------

.PHONY: check lint format typecheck vet check-dtos check-css-var-parens
check: lint typecheck vet i18n-check check-dtos check-css-var-parens ## Lint + typecheck + go vet + i18n locale guard + DTO drift guard + CSS var() paren guard

check-dtos: ## Fail when web routes/features hand-roll response DTOs instead of using SDK schemas
	bash scripts/check-handrolled-dtos.sh

check-css-var-parens: ## Fail when a var(--nf-...) token reference has a stray extra closing paren
	bash scripts/check-css-var-parens.sh --ci

lint: ## biome check + golangci-lint
	$(PKG_RUN) check
	cd apps/flow-api && golangci-lint run ./...
	cd apps/auth-api && golangci-lint run ./...

format: ## biome format + gofmt
	$(PKG_RUN) format
	cd apps/flow-api && gofmt -w .
	cd apps/auth-api && gofmt -w .

typecheck: ## tsc -b
	$(PKG_RUN) typecheck

vet: ## go vet
	cd apps/flow-api && go vet ./...
	cd apps/auth-api && go vet ./...

# ---------- codegen ----------

.PHONY: gen gen-sqlc gen-errors gen-signal-kinds gen-sdk gen-openapi i18n-check
gen: gen-sqlc gen-errors gen-signal-kinds gen-sdk ## Run all codegen (sqlc + errors + signal-kinds + sdk)

gen-sqlc: ## sqlc generate (requires the pinned sqlc version)
	@bash scripts/check-codegen-drift.sh --check-tool
	sqlc generate -f sql/sqlc.yaml

gen-errors: ## Regenerate Go/TS error modules + locale stubs + docs from errors/*.yaml
	go -C scripts run gen-errors.go

gen-signal-kinds: ## Regenerate Go/TS signal-kind modules + locale stubs + docs from signal_kinds/*.yaml
	go -C scripts run gen-signal-kinds.go

i18n-check: ## Fail on missing ja keys, empty string values, or i18next-native '{{var}}' placeholders under the ICU backend
	node scripts/i18n-translate.mjs --check

gen-openapi: ## Dump merged OpenAPI 3.1 (flow-api + auth-api) to packages/sdk/openapi.json
	cd apps/flow-api && go run ./cmd/dump-openapi -o ../../packages/sdk/openapi-flow.json
	cd apps/auth-api && go run ./cmd/dump-openapi -o ../../packages/sdk/openapi-auth.json
	cd scripts && go run merge-openapi.go -o ../packages/sdk/openapi.json ../packages/sdk/openapi-flow.json ../packages/sdk/openapi-auth.json
	$(PKG_X) biome format --write packages/sdk/openapi.json
	@rm -f packages/sdk/openapi-flow.json packages/sdk/openapi-auth.json

gen-sdk: gen-openapi ## Generate TS SDK from OpenAPI
	cd packages/sdk && $(PKG_X) openapi-typescript openapi.json -o src/openapi.ts

# ---------- database ----------

# NF_DB_DSN is consumed by `make db-apply` when applying the schema to a
# self-hosted MySQL. For compose, schema.sql is auto-applied by the mysql
# container on first boot (empty volume). Override NF_DB_HOST/PORT/USER/
# PASSWORD/NAME for one-off usage without editing .env.
NF_DB_HOST     ?= 127.0.0.1
NF_DB_PORT     ?= 3306
NF_DB_USER     ?= nodate
NF_DB_PASSWORD ?= nodatepw
NF_DB_NAME     ?= nodate_flow

.PHONY: db-schema db-apply db-reset db-shell seed-flow seed-calendar
db-schema: ## Regenerate sql/schema.sql from sql/core + sql/flow
	bash sql/build-schema.sh > sql/schema.sql

db-apply: db-schema ## Apply schema.sql to a self-hosted MySQL (uses NF_DB_* vars)
	@echo "applying sql/schema.sql to $(NF_DB_USER)@$(NF_DB_HOST):$(NF_DB_PORT)/$(NF_DB_NAME)"
	mysql -h $(NF_DB_HOST) -P $(NF_DB_PORT) -u $(NF_DB_USER) -p$(NF_DB_PASSWORD) $(NF_DB_NAME) < sql/schema.sql

db-reset: db-schema ## Drop the compose mysql volume and re-init from schema.sql
	docker compose down
	docker volume rm nodate-flow_mysql_data || true
	docker compose up -d mysql

db-shell: ## Open a mysql shell against the compose mysql
	docker compose exec mysql mysql -u $(NF_DB_USER) -p$(NF_DB_PASSWORD) $(NF_DB_NAME)

seed-flow: ## Insert dev admin user + demo workspace (idempotent; NF_SEED_LOCALE=en|ja)
	@dsn="$${NF_DB_DSN:-$(NF_DB_USER):$(NF_DB_PASSWORD)@tcp($(NF_DB_HOST):$(NF_DB_PORT))/$(NF_DB_NAME)?parseTime=true&charset=utf8mb4&collation=utf8mb4_0900_ai_ci}"; \
	cd apps/flow-api && NF_DB_DSN="$$dsn" go run ./cmd/seed-dev

seed-calendar: ## Seed calendar demo data via REST API (NF_SEED_LOCALE=en|ja)
	./scripts/seed-calendar.sh

# ---------- clean ----------

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin apps/flow-web/dist packages/sdk/dist packages/holidays/dist
