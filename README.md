# nodate-flow

> **Not a tool to manage tasks — a system where work flows forward on its own.**

nodate-flow is an OSS task platform that starts as a lightweight Asana
alternative and evolves, natively, into something different: a system where
LLMs and MCP are not bolted-on features but the execution layer itself.

> **About the name:** "nodate-flow" is a pun on English *"no date"* (work
> that isn't ruled by deadlines) and Japanese **"野点 (nodate)"** — the
> practice of informally preparing tea outdoors. The name evokes work that
> flows lightly and unhurriedly, rather than being driven by rigid due
> dates.

**Status:** alpha — under active development, not yet released.
**License:** [AGPL-3.0](./LICENSE).
**Stack:** Go (Huma + chi + sqlc) · MySQL 9.1 · React 19 (Vite + TanStack + Tailwind v4) · OpenAPI 3.1 boundary.

---

## What we're building

Most task managers treat a task as a **row** — a record with a `status`
column that humans click through. nodate-flow treats a task as a **process**:
a small autonomous unit whose state is *derived* from constraints and an
event log, not assigned by a button press.

Five design principles sit above every feature decision:

1. **A task is a process, not a row.** State is derived, not stored.
2. **Drive by constraints and events, not by status updates.** No direct
   `UPDATE tasks SET status = ...`.
3. **LLMs are execution logic, not garnish.** The system works without an
   LLM, but behaves dramatically differently with one.
4. **External services share the same space via MCP.** GitHub issues, Slack
   threads, Google Docs — all land in a unified `signals` table, not behind
   bespoke integration code.
5. **The UI is for observation and intervention, not operation.** Timelines
   and state graphs first; buttons second.

## How it differs from existing products

| | Asana / Jira / Linear | Plane / Vikunja / OpenProject | **nodate-flow** |
|---|---|---|---|
| Task model | Row with `status` | Row with `status` | Process derived from constraints + events |
| LLM role | Bolt-on "AI features" | Mostly absent | Core execution logic |
| External services | REST integrations | Webhooks / REST | MCP as a first-class signal source |
| UI philosophy | Operate (click to change state) | Operate | Observe + intervene |
| Hosting model | Proprietary SaaS | OSS, self-host friendly | OSS (AGPL), self-host first |
| License | Proprietary | AGPL-3.0 (Plane, Vikunja) / GPL (OpenProject) | AGPL-3.0 |

**Closest peer:** Plane. nodate-flow shares the "OSS Asana-alike, AGPL,
self-host-first" positioning, but diverges on the task model itself —
Plane is still row-and-status; nodate-flow is constraints-and-events with
LLM as a built-in actor.

**Not trying to be:** a pixel-perfect Asana clone. Heavy add-ons like
Cycle / Module / Goal / OKR / Portfolio / Form / Wiki / Gantt are
deliberately **out of scope** — the bet is that most of them either emerge
naturally from a constraint + event model, or turn out to be unnecessary.

## Repository layout

```
apps/
  api/       # Go backend (Huma + chi + sqlc)
  web/       # React 19 frontend (Vite + TanStack)
  cli/       # TypeScript CLI (binary: tnk)
packages/
  sdk/       # Generated TypeScript SDK (from OpenAPI)
  ui/        # Design system
  fixtures/  # Test fixtures
errors/      # YAML — single source for error codes (→ Go + TS codegen)
sql/         # Tables, views, sqlc queries
```

## Getting started

Alpha — no stable releases yet. Development entry point:

```sh
git clone <repo>
cd nodate-flow
cp .env.example .env
docker compose up
```

## Licensing

nodate-flow is licensed under **AGPL-3.0**. You are free to self-host,
modify, and redistribute. If you run a modified version as a network
service, AGPL §13 requires you to make the corresponding source available
to its users — the same model used by Plane, Vikunja, and Leantime.
