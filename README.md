# nodate-flow

> **Not a tool to manage tasks — a system where work flows forward on its own.**

An OSS task platform where LLMs and MCP are not add-ons but the execution
layer. Tasks are processes whose state is derived from constraints and
events, not rows updated by button clicks.

A single monorepo ships two products served by **one combined backend** (`flow-api`):

- **nodate-flow** — task management driven by constraints, events, and AI agents.
- **nodate-time** — a calendar that blends TimeTree's shared-calendar simplicity with Google Calendar's permission model.

> **About the name:** a pun on *"no date"* (work free from deadline pressure) and Japanese **野点 (nodate)** — preparing tea outdoors, unhurried.

**Status:** alpha | **License:** [AGPL-3.0](./LICENSE) | **Stack:** Go · MySQL 9.6 · React 19 · OpenAPI 3.1

---

## nodate-flow

| Feature | Detail |
|---|---|
| **Views** | Board, timeline, Gantt, dashboard, custom lenses |
| **Constraints** | Dependencies, auto-evaluation, derived state — no manual status toggling |
| **AI** | Priority & state suggestions, smart create, embedding-based duplicate detection |
| **MCP** | Built-in server — GitHub, Slack, and external tools live in the same workspace |
| **Pages** | Lightweight wiki / docs |
| **Notifications** | Inbox with weekly digest |
| **Webhooks** | Outbound event delivery |
| **Auth** | Password (Argon2id) · OIDC · TOTP 2FA · recovery codes |
| **Multi-tenant** | Workspaces with role-based access control |

## nodate-time

| Feature | Detail |
|---|---|
| **Calendars** | Shared & personal, per-member visibility control |
| **Events** | Attendees, invites, checklists, comments, attachments |
| **Sync** | Two-way task sync with nodate-flow |
| **Holidays** | Locale-aware holiday scheduling |

## Why not Asana / Plane / Linear?

Most tools treat a task as a database row you click through.
nodate-flow treats it as a **process** — state is derived from constraints
and an event log, LLMs act as built-in execution actors, and external
services land in the workspace via MCP rather than bespoke integrations.

Closest peer is [Plane](https://plane.so) (OSS, AGPL, self-host-first),
but Plane stays in the row-and-status paradigm.

## Quick start

```sh
git clone <repo> && cd nodate-flow
make dev          # MySQL (Docker) + auth-api + flow-api + flow-web
make seed-flow    # demo admin user + workspace
```

```
make reload       # stop-dev (kill by port) + dev, for a clean restart
make test-unit    # Go + TS suites that need no container
make test         # Go + TS tests
make gen          # codegen (sqlc + errors + SDK)
make help         # all targets
```

## Repo structure

```
apps/
  flow-api/       # Go backend — tasks, calendar, AI, MCP, public shares (Huma + chi + sqlc)
  flow-web/       # React 19 frontend — tasks, calendar, /share/cal, /invites/accept, /setup
  auth-api/       # Go — auth & sessions (JWT, OIDC, TOTP)
  accounts-web/   # React 19 — login / signup / account UI
  cli/            # CLI (binary: tnk)
packages/
  sdk/            # TS SDK for flow-api (generated from OpenAPI)
  ui/             # Design system (4 themes)
  go-shared/      # Shared Go packages
  holidays/       # Holiday data
  fixtures/       # Test fixtures
errors/           # Error codes (YAML → Go + TS codegen)
sql/              # Tables, views, sqlc queries
infra/            # Docker, Prometheus, Grafana, OTel, Caddy
```

## License

[AGPL-3.0](./LICENSE) — self-host, modify, redistribute freely. Network
use requires source availability (same model as Plane and Vikunja).
