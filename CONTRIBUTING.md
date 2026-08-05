# Contributing to nodate-flow

Thank you for your interest in contributing to nodate-flow. This document
covers the practical steps to get a development environment running and
submit changes.

## Prerequisites

- **Go 1.23+**
- **Node.js 20+** with **Bun** (preferred) or npm
- **Docker** and **Docker Compose** (for MySQL, Redis, MinIO)
- **Make**

## Development setup

```sh
git clone <repo>
cd nodate-flow
cp .env.example .env          # created automatically by `make dev` if missing
docker compose up -d           # starts MySQL, Redis, MinIO
make dev                       # runs Go API + React frontend in parallel
```

`make help` lists all available targets. The most common ones:

| Command | What it does |
|---|---|
| `make dev` | Start MySQL (compose) + API + web in parallel |
| `make dev-api` | Run Go API only |
| `make dev-web` | Run Vite dev server only |
| `make lint` | Run all linters (golangci-lint + biome) |
| `make test` | Run all tests |
| `make gen-errors` | Regenerate Go + TS error code files from `errors/*.yaml` |
| `make gen-sqlc` | Regenerate sqlc Go types from `sql/` |
| `make gen-sdk` | Regenerate TypeScript SDK from OpenAPI spec |
| `make verify-codegen` | Fail when the schema and its generated code disagree |

## Design order

nodate-flow follows a strict **DB -> API -> UI** design sequence. Schema
changes come first, then API endpoints, then frontend. Never work backwards.
If a feature requires a deviation from this order, open an ADR.

## Code style

All source code, database comments, log messages, and commit messages are
written in **English**. Only UI-facing strings are internationalized.

Detailed conventions live in `docs/conventions/`:

- [Code style](./docs/conventions/code-style.md) -- language, naming, comments
- [Case conventions](./docs/conventions/case.md) -- snake_case / camelCase / PascalCase rules
- [Error codes](./docs/conventions/errors.md) -- `DOMAIN.RESOURCE.REASON` format
- [Database](./docs/conventions/db.md) -- schema rules, sqlc usage
- [Testing](./docs/conventions/testing.md) -- test layers, philosophy
- [i18n](./docs/conventions/i18n.md) -- translation key structure
- [API types](./docs/conventions/api-types.md) -- `*_at` (unixtime) vs `*_on` (date string)
- [Secrets](./docs/conventions/secrets.md) -- handling sensitive values
- [Frontend](./docs/conventions/frontend.md) -- React, TanStack, Zustand patterns

## Pull request process

1. **Branch from `main`**. Use a descriptive branch name
   (e.g. `feat/task-archive`, `fix/login-race`).

2. **Follow Conventional Commits**. Commit messages must be English, lowercase
   subject, 72 characters max. Types: `feat`, `fix`, `docs`, `style`,
   `refactor`, `test`, `chore`, `perf`, `build`, `ci`, `revert`. No emoji.

   ```
   feat(tasks): add archive endpoint

   Adds POST /tasks/:id/archive that emits a task.archived event.
   The derived_state is updated by the constraint engine, not directly.
   ```

3. **CI must be green**. The pre-commit hook runs biome (TS) and lint-staged
   automatically. CI runs the full suite: biome, golangci-lint, typecheck,
   and all test layers. `--no-verify` is not allowed.

4. **Include tests**. Every feature PR must include corresponding tests.
   Test-less feature PRs will not be merged.

5. **One concern per PR**. Keep PRs focused and reviewable.

## Linters

**Go:**

```sh
cd apps/flow-api && golangci-lint run ./...
```

**TypeScript:**

```sh
bun run check          # biome check + typecheck
bun run lint           # biome lint only
bun run format         # biome format only
```

Both run automatically via git hooks (pre-commit) and CI. Do not bypass them.

## Testing

nodate-flow uses real infrastructure for tests -- no database mocks.

**Backend (Go):** `go test` with `testify` and `testcontainers-go` for MySQL,
Redis, and MinIO. API E2E tests live in `apps/flow-api/tests/`.

**Frontend (TypeScript):** Vitest with `@testing-library/react` and `happy-dom`
for component tests. Playwright for browser E2E.

Tests must be parallel-safe. Use `createTestTenant` / `cleanupTenant` helpers
to isolate test data. Never depend on execution order.

```sh
make test              # run everything
make test-api          # Go tests only
make test-web          # frontend tests only
```

## Error codes

Error codes are defined in `errors/*.yaml` (the single source of truth) and
generated into Go constants and TypeScript types via codegen.

To add or modify an error code:

1. Edit the relevant YAML file in `errors/` (e.g. `errors/auth.yaml`).
2. Run `make gen-errors` to regenerate Go and TS files.
3. Never hand-edit files in `apps/flow-api/internal/errors/` or
   `packages/sdk/src/errors/` -- they are generated.

Error codes follow the `DOMAIN.RESOURCE.REASON` format. See
[docs/conventions/errors.md](./docs/conventions/errors.md) for naming rules.

## i18n

All user-facing strings in the web frontend go through `t('key')` via
`react-i18next`. Hardcoded UI strings are not allowed.

Translation files live in `apps/flow-web/locales/`. See
[docs/conventions/i18n.md](./docs/conventions/i18n.md) for the full guide.

## Generated files

Several directories contain generated code. These are marked with
"DO NOT EDIT" headers and must never be modified by hand:

- `apps/flow-api/internal/db/generated/` -- sqlc output
- `apps/flow-api/internal/errors/` -- error code codegen output
- `packages/sdk/` -- OpenAPI TypeScript SDK

### A schema change is not finished until the generated code follows

`sql/schema.sql` and the sqlc output are both derived from the layered
sources under `sql/core/` and `sql/flow/`. Neither is regenerated
automatically, and leaving one behind is quiet: a column that exists in
the schema but not in the generated struct is not a compile error, so
the build succeeds and the tests pass while the column is unreachable.

Column comments count too -- sqlc copies them into the generated Go doc
comments, so a comment-only edit still leaves the generated files stale.

A change under `sql/` therefore runs both generators before it is done:

```sh
make db-schema   # layered sources -> sql/schema.sql
make gen-sqlc    # schema + queries -> apps/*/internal/db/generated/
```

`make verify-codegen` checks the pairing without writing anything, and
is what the pre-commit hook and CI both run. The generator version is
pinned in `scripts/check-codegen-drift.sh`, because two sqlc versions
emit different code from identical input.

### Hooks

The pre-commit hook is `.githooks/pre-commit`, tracked in this
repository and reached through the global hooks directory. When
`core.hooksPath` is configured, git ignores `.git/hooks` entirely, so
anything a package manager installs there never runs; `git rev-parse
--git-path hooks` shows which directory git actually reads.

## License

By contributing to nodate-flow, you agree that your contributions will be
licensed under [AGPL-3.0](./LICENSE). Do not add license headers or SPDX
identifiers to individual files -- the root `LICENSE` file covers everything.
