# Web E2E (Playwright)

Phase 1 closeout smoke harness for `apps/web`. Real backend, real DB, no
mocks (per `CLAUDE.md` rule 7).

## Layout

```
apps/web/
  playwright.config.ts        # chromium project, vite webServer
  e2e/
    README.md                 # this file
    auth.spec.ts              # happy-path smoke test
    fixtures/
      tenant.ts               # REST helper: createTestTenant / cleanupTenant
```

## Prerequisites

1. Backend stack up (MySQL + Redis + MinIO + API):

   ```sh
   cd apps/api
   go run ./cmd/api
   ```

   The API must be reachable at `NF_API_URL` (default
   `http://localhost:8080`).

2. Browsers installed once per machine:

   ```sh
   bunx playwright install chromium
   ```

The Vite dev server is started automatically by Playwright's `webServer`
block; you do **not** need to run `bun run dev` separately.

## Running

From `apps/web/`:

```sh
bun run e2e          # headless
bun run e2e:ui       # Playwright UI mode
```

Override URLs if needed:

```sh
NF_API_URL=http://localhost:18080 NF_WEB_URL=http://localhost:5173 bun run e2e
```

## Conventions

- One tenant per test, created via REST with a `crypto.randomUUID()` email.
- Setup/teardown go through the public API; only assertions touch the UI.
- Tests run fully parallel. Do not introduce shared state.
- `retries = 0` locally, `2` in CI. Trace is captured `on-first-retry`.

## Troubleshooting

- **`POST /auth/register -> 000`**: backend not running. Start it as above.
- **Vite port conflict**: another dev server is on `:5173`. Stop it or set
  `NF_WEB_URL` to a free port and pass `--port` to vite.
- **Selectors drift**: the smoke test uses role/label queries. If the
  signup form labels change, update `auth.spec.ts` to match.
