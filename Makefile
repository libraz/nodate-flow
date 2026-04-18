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

# Package manager used for JS scripts. Override with `make PKG=bun dev` etc.
PKG ?= bun
PKG_RUN := $(PKG) run
ifeq ($(PKG),npm)
PKG_X := npx --yes
else
PKG_X := $(PKG) x
endif

.DEFAULT_GOAL := help

# ---------- meta ----------

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---------- dev (yarn dev equivalent) ----------

.PHONY: dev dev-api dev-web dev-time-api dev-time up down logs
dev: db-schema .env ## Start MySQL (compose) + flow API + flow web in parallel
	@echo "starting mysql, flow-api, flow-web..."
	@docker compose up -d mysql
	@$(MAKE) -j2 dev-api dev-web

dev-time: db-schema .env ## Start MySQL (compose) + time API + time web in parallel
	@echo "starting mysql, time-api..."
	@docker compose up -d mysql
	@$(MAKE) dev-time-api

# Copy .env.example on first run so `make dev` works out of the box.
.env:
	@echo "creating .env from .env.example"
	cp .env.example .env
	@# Default DSN in .env.example targets the compose "mysql" host, but
	@# `make dev` runs the api on the host so point it at 127.0.0.1.
	@sed -i.bak 's|@tcp(mysql:3306)|@tcp(127.0.0.1:3306)|' .env && rm -f .env.bak

dev-api: ## Run Go API against the local MySQL (reads .env)
	cd apps/flow-api && go run ./cmd/api

dev-web: ## Run Vite dev server
	cd apps/flow-web && $(PKG_RUN) dev

dev-time-api: ## Run nodate-time API against the local MySQL (reads .env)
	cd apps/time-api && go run ./cmd/api

up: ## docker compose up -d (full stack)
	docker compose up -d

down: ## docker compose down
	docker compose down

logs: ## Tail compose logs
	docker compose logs -f

# ---------- build ----------

.PHONY: build build-api build-web
build: build-api build-time-api build-web ## Build all apps

build-api:
	cd apps/flow-api && go build -o ../../bin/flow-api ./cmd/api

build-time-api:
	cd apps/time-api && go build -o ../../bin/time-api ./cmd/api

build-web:
	cd apps/flow-web && $(PKG_RUN) build

# ---------- test ----------

.PHONY: test test-api test-web test-e2e test-contract lighthouse
test: test-api test-time-api test-web ## Run unit/integration tests (Go + TS)

test-api: ## Go tests (flow)
	cd apps/flow-api && go test ./...

test-time-api: ## Go tests (time)
	cd apps/time-api && go test ./...

test-web: ## Vitest
	cd apps/flow-web && $(PKG_RUN) test

test-e2e: ## Playwright E2E
	cd apps/flow-web && $(PKG_RUN) e2e

test-contract: ## Schemathesis contract tests (requires running API)
	./scripts/contract-test.sh

lighthouse: build-web ## Run Lighthouse CI (a11y 95+, perf 70+)
	$(PKG_X) @lhci/cli autorun

# ---------- lint / format / typecheck ----------

.PHONY: check lint format typecheck vet
check: lint typecheck vet ## Lint + typecheck + go vet

lint: ## biome check + golangci-lint
	$(PKG_RUN) check
	cd apps/flow-api && golangci-lint run ./... || true

format: ## biome format + gofmt
	$(PKG_RUN) format
	cd apps/flow-api && gofmt -w .

typecheck: ## tsc -b
	$(PKG_RUN) typecheck

vet: ## go vet
	cd apps/flow-api && go vet ./...
	cd apps/time-api && go vet ./...

# ---------- codegen ----------

.PHONY: gen gen-sqlc gen-errors gen-sdk gen-openapi
gen: gen-sqlc gen-errors gen-sdk ## Run all codegen (sqlc + errors + sdk)

gen-sqlc: ## sqlc generate (requires sqlc installed)
	sqlc generate -f sql/sqlc.yaml

gen-errors: ## Regenerate Go/TS error modules + locale stubs + docs from errors/*.yaml
	go -C scripts run gen-errors.go

gen-openapi: ## Dump merged OpenAPI 3.1 to packages/sdk/openapi.json
	cd apps/flow-api && go run ./cmd/dump-openapi -o ../../packages/sdk/openapi.json

gen-sdk: gen-openapi ## Generate TS SDK from OpenAPI
	cd packages/sdk && $(PKG_X) openapi-typescript openapi.json -o src/openapi.ts

gen-time-openapi: ## Dump nodate-time OpenAPI 3.1 to packages/time-sdk/openapi.json
	cd apps/time-api && go run ./cmd/dump-openapi -o ../../packages/time-sdk/openapi.json

gen-time-sdk: gen-time-openapi ## Generate nodate-time TS SDK from OpenAPI
	cd packages/time-sdk && $(PKG_X) openapi-typescript openapi.json -o src/openapi.ts

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

.PHONY: db-schema db-apply db-reset db-shell seed-flow seed-time
db-schema: ## Regenerate sql/schema.sql from sql/tables + sql/views
	bash sql/build-schema.sh > sql/schema.sql

db-apply: db-schema ## Apply schema.sql to a self-hosted MySQL (uses NF_DB_* vars)
	@echo "applying sql/schema.sql to $(NF_DB_USER)@$(NF_DB_HOST):$(NF_DB_PORT)/$(NF_DB_NAME)"
	mysql -h $(NF_DB_HOST) -P $(NF_DB_PORT) -u $(NF_DB_USER) -p$(NF_DB_PASSWORD) $(NF_DB_NAME) < sql/schema.sql

db-reset: ## Drop the compose mysql volume and re-init from schema.sql
	docker compose down
	docker volume rm nodate-flow_mysql_data || true
	docker compose up -d mysql

db-shell: ## Open a mysql shell against the compose mysql
	docker compose exec mysql mysql -u $(NF_DB_USER) -p$(NF_DB_PASSWORD) $(NF_DB_NAME)

seed-flow: ## Insert dev admin user + demo workspace (idempotent; ND_SEED_LOCALE=en|ja)
	@dsn="$${NF_DB_DSN:-$(NF_DB_USER):$(NF_DB_PASSWORD)@tcp($(NF_DB_HOST):$(NF_DB_PORT))/$(NF_DB_NAME)?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci}"; \
	cd apps/flow-api && NF_DB_DSN="$$dsn" go run ./cmd/seed-dev

seed-time: ## Seed nodate-time calendar demo data via REST API (ND_SEED_LOCALE=en|ja)
	./scripts/seed-time.sh

# ---------- clean ----------

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin apps/flow-web/dist packages/sdk/dist packages/holidays/dist
