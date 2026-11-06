# Contributing to nodate-flow

Thank you for your interest in nodate-flow. Small, partial and unfinished
changes are welcome, and so are bug reports with no fix. The maintainer
reviews every pull request, finishes what is missing and merges it, so you
do not need to satisfy or even read everything below.

## What a pull request needs

1. A description of what the change does and why. A clear intent is enough
   for the maintainer to verify the behaviour.
2. If you can, a build and the tests for the part you changed.

A red check, a missing translation or test, or an untouched generated file
is fine. Target `main`. Opening an issue first is optional and mostly
useful for a larger change.

### What the maintainer can verify

The maintainer tests on macOS, with the interface in English and Japanese,
and on Windows in Japanese only. For a bug that depends on another OS or
display language (keyboard layout, IME, system font, locale-specific
behaviour), describe what you see, ideally with a screenshot, because it
cannot be reproduced here.

## Development setup

Requires Go, Bun, Docker and Make. The Go version is pinned in `go.mod` and
the Bun version in the `packageManager` field of `package.json`. Bun is the
only package manager used; do not call `npm`, `pnpm`, `yarn` or `npx`.

```sh
git clone <repo>
cd nodate-flow
make dev          # creates .env if missing, then starts everything below
make seed-flow    # in a second terminal: demo admin user and workspace
```

`make dev` starts MySQL in Docker (`docker compose up -d mysql`, with the
schema applied on first boot) plus auth-api, flow-api, accounts-web and
flow-web on the host. It does not start Redis, object storage, the mail
catcher, the observability stack, flow-worker or the Discord gateway. The
default event stream and rate limiter run in process, so Redis is not
needed to develop. `make dev-worker` and `make dev-presence` run the worker
and the gateway natively. `docker compose up -d` starts every non-optional
service in `compose.yml` (MySQL, Redis-compatible store, object storage,
mail catcher, OpenTelemetry collector, Prometheus, Grafana, and containers
for flow-api and auth-api).

`make help` lists all targets. The common ones:

| Command               | What it does                                                                         |
| --------------------- | ------------------------------------------------------------------------------------ |
| `make dev`            | Start MySQL (compose) + auth-api + flow-api + accounts-web + flow-web                |
| `make dev-api`        | Run flow-api only                                                                    |
| `make dev-web`        | Run the flow-web Vite dev server only                                                |
| `make dev-reset`      | Drop the database volume, rebuild the schema, seed and start again                   |
| `make lint`           | Run all linters (golangci-lint + biome)                                              |
| `make check`          | Static guards only -- lint, formatting, typecheck, `go vet`, locale and token guards |
| `make test-unit`      | Every suite that needs no container (Go + TS)                                        |
| `make test`           | Run all tests, including the container-backed suites                                 |
| `make gen-errors`     | Regenerate Go + TS error code files from `errors/*.yaml`                             |
| `make gen-sqlc`       | Regenerate sqlc Go types from `sql/`                                                 |
| `make gen-sdk`        | Regenerate the TypeScript SDK from the OpenAPI spec                                  |
| `make verify-codegen` | Fail when the schema and its generated code disagree                                 |

## CI

CI runs on pull requests to `main` and on pushes to `main`
(`.github/workflows/ci.yml`). A separate scheduled workflow
(`.github/workflows/vendored-drift.yml`) checks the vendored schema layer
against upstream. A red check is no reason to hold a pull request back.

| Job                           | What it runs                                                               |
| ----------------------------- | -------------------------------------------------------------------------- |
| Web (format, lint, test)      | biome, typecheck, i18n, token and string guards, TS unit tests, web builds |
| OpenAPI (spec + SDK drift)    | The spec and SDK match the Go sources                                      |
| Schema (drift, core contract) | Schema drift, generated-code drift, core contract suites                   |
| API (format, lint, test)      | gofmt, golangci-lint, `go vet`, build, Go tests for every module           |
| Images (prod compose)         | `compose.prod.yml` validates and the images build                          |

## Checklist

A merged change looks like this. Anything missing, the maintainer fills in.

- Code, comments, database comments, log messages and commit messages are in English; only UI strings are translated.
- Work goes in the order DB, then API, then UI: schema first, endpoints next, frontend last.
- Case conversion happens in one place: sqlc maps snake_case columns to Go structs, and JSON tags give camelCase. No hand-written converters.
- Errors are `apierror.New(errors.<CODE>, ...)` with a code from `errors/*.yaml` (`DOMAIN.RESOURCE.REASON`), then `make gen-errors`. No `errors.New` for API errors and no literal status codes.
- Database access is raw SQL through sqlc. No ORM.
- `tasks.derived_state` is never updated directly; it derives from events.
- Rows carry an internal `id` for joins and a `public_id` (UUID v7) for anything exposed. User-created data never exposes the internal id.
- API timestamps are `*_at` as unix seconds (int64) and `*_on` as `YYYY-MM-DD` strings. Conversion happens in the mapper only.
- UI strings go through `t('key')`, with keys in the locale files. No hardcoded text.
- CSS uses the design tokens (`var(--nf-color-*)`, `--nf-space-*`, `--nf-radius-*`, and so on). No literal colours or spacing.
- Tests run in parallel against a real database, isolated with `createTestTenant` / `cleanupTenant`. No database mocks.
- Secrets never reach logs, API responses or error messages.
- Non-ASCII text is written as UTF-8, not as escapes or character references. Files are LF without a BOM.
- A change under `sql/` regenerates `sql/schema.sql` and the sqlc output in the same commit.

## Reference

Nothing below asks anything of you; it explains how the pieces behave.

### Commits

Commit messages follow Conventional Commits: English, lowercase subject,
72 characters at most. Types: `feat`, `fix`, `docs`, `style`, `refactor`,
`test`, `chore`, `perf`, `build`, `ci`, `revert`. No emoji. The maintainer
can reword a message at merge.

```
feat(tasks): add archive endpoint

Adds POST /tasks/:id/archive that emits a task.archived event.
The derived_state is updated by the constraint engine, not directly.
```

### Linters

```sh
cd apps/flow-api && golangci-lint run ./...   # Go, per module
bun run check                                  # biome check
bun run typecheck                              # tsc -b
bun run format                                 # biome format --write
```

`make lint`, `make check` and `make typecheck` run these across the
repository.

### Tests

The backend uses `go test` with `testify` and `testcontainers-go` for
MySQL, Redis and S3-compatible storage. The frontend uses Vitest with
`@testing-library/react` and Playwright for browser E2E. Never depend on
execution order.

```sh
make test-unit         # everything that needs no container
make test              # the above plus the container-backed suites
make test-api          # flow-api tests only
make test-web          # flow-web tests only
make test-e2e          # Playwright
```

The suites come in tiers, and the tier decides where each one can run:

| Tier             | What it is                                                                                                    | Where it runs                                                       |
| ---------------- | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| `make check`     | Static guards. Reads the sources, runs no behaviour.                                                          | Locally, and step by step in CI                                     |
| `make test-unit` | Every suite that needs no container. No `-race`, and `-short` skips the whole-module source scanners.         | Locally, and in the pre-commit hook, narrowed to the staged changes |
| `make test`      | The above plus the suites that boot MySQL and S3 storage. Minutes, and they contend for the container reaper. | Locally on demand, and in full in CI                                |

Nothing a faster tier skips is skipped everywhere: CI runs every suite
with `-race` and without `-short`.

### Error codes

`errors/*.yaml` is the single source. `make gen-errors` generates the Go
constants, the TypeScript types and the locale stubs. Never hand-edit
`apps/flow-api/internal/errors/`, `apps/auth-api/internal/errors/` or
`packages/sdk/src/errors/`.

### Generated files

These carry "DO NOT EDIT" headers and are never modified by hand:

- `apps/*/internal/db/generated/` -- sqlc output
- `apps/*/internal/errors/` -- error code codegen output
- `packages/sdk/` -- OpenAPI TypeScript SDK

`sql/schema.sql` and the sqlc output are both derived from the layered
sources under `sql/core/` and `sql/flow/`. Neither regenerates
automatically, and leaving one behind is quiet: a column that exists in
the schema but not in the generated struct is not a compile error, so the
build and the tests pass while the column is unreachable. Column comments
count too, because sqlc copies them into the generated Go doc comments.

```sh
make db-schema   # layered sources -> sql/schema.sql
make gen-sqlc    # schema + queries -> apps/*/internal/db/generated/
```

`make verify-codegen` checks the pairing without writing anything. CI runs
the whole target; the pre-commit hook runs each check behind a gate on its
own inputs. The generator version is pinned in
`scripts/check-codegen-drift.sh`, because two sqlc versions emit different
code from identical input.

### Hooks

The pre-commit hook is `.githooks/pre-commit`, tracked in this repository
and reached through the global hooks directory. When `core.hooksPath` is
configured, git ignores `.git/hooks` entirely, so anything a package
manager installs there never runs; `git rev-parse --git-path hooks` shows
which directory git actually reads.

Every check in it is scoped to what was staged. It formats and lints the
staged files, runs the codegen checks whose inputs are among them, and
runs the test suites those files can break. Container-backed suites stay
out. `--no-verify` is not allowed. The suites exercise the working tree,
not the staged content, so a partially staged file is tested as it sits on
disk.

## Reporting issues

Bug reports with no fix are contributions. Please include the OS and its
display language, the browser (for web problems), the nodate-flow commit,
and the steps to reproduce. For a security issue, use
[SECURITY.md](./SECURITY.md) instead of a public issue.

## License

By contributing to nodate-flow, you agree that your contributions will be
licensed under [AGPL-3.0](./LICENSE). Do not add license headers or SPDX
identifiers to individual files -- the root `LICENSE` file covers everything.
