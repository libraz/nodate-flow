# nodate-flow v1.0.0

**Release date:** 2026-04-XX

nodate-flow is an open-source task platform that treats tasks as processes
rather than rows. Instead of clicking through status columns, task state is
derived from constraints and an event log. LLMs and MCP are not bolt-on
features but part of the execution layer itself. v1.0.0 is the first stable
release, covering the full stack from constraint-driven automation to AI
intelligence, real-time collaboration, and five distinct view modes.

---

## Highlights

### Constraint-driven task automation

Tasks in nodate-flow do not have a manually-assigned status. State is derived
from constraints (dependencies, timeboxes, assignee rules) and an append-only
event log. When a blocking task completes, the constraint engine evaluates all
dependents and transitions them automatically. This removes the entire class
of "forgot to update the ticket" problems and makes every state change
auditable back to the event that caused it.

### AI intelligence layer

A built-in AI layer turns natural language into action. The command palette
accepts plain-language instructions ("create a task to fix the login bug and
assign it to backend"), resolves them to MCP tool calls, and executes with
user confirmation. Background pipelines detect duplicate or related tasks via
embedding similarity, infer task state from signals, suggest priority
adjustments, and generate wiki pages from project context. A weekly digest
summarizes workspace activity.

### MCP server with 23 tools

nodate-flow ships a Model Context Protocol server accessible over Streamable
HTTP. The 23 tools span task management, AI operations, timeboxes, pages,
and data export. Any MCP-compatible AI agent or IDE can connect to a
nodate-flow workspace and operate on its data with the same permission model
as human users.

### Real-time collaboration

Server-Sent Events push task updates, constraint evaluations, and signal
arrivals to connected browsers in real time. A notification system with
AI-powered importance filtering delivers alerts via in-app, email, and web
push channels. Webhook subscriptions with HMAC-SHA256 signatures and
exponential-backoff retries allow external systems to react to any workspace
event.

### Flexible views

Five view modes present task data in different shapes: board (kanban),
list, dependency graph, spreadsheet (with inline editing, bulk operations,
and constraint violation previews), and a widget-based dashboard. Dashboard
widgets include task summary, burndown chart, signals feed, AI suggestions,
overdue tasks, and notification feed, all repositionable via drag-and-drop
with AI-suggested initial layouts.

---

## Full feature list

### Core

- Workspaces with member management and role-based access (owner, admin, member, guest)
- Projects with customizable task states and state categories
- Tasks with title, description, priority, assignees, due dates, and custom fields
- Task dependencies (blocks, blocked-by, relates-to)
- Comments and activity timeline
- Lenses (saved filter/sort/group views)
- Signals: unified event log from internal actions and external integrations (GitHub, Slack)
- Dual-ID system: internal auto-increment for performance, public UUID v7 for API exposure

### Views

- Board (kanban) with drag-and-drop state transitions
- List with sortable/filterable columns
- Dependency graph visualization
- Spreadsheet with inline editing, tab navigation, and bulk operations
- Dashboard with six widget types, drag-and-drop layout, and resize

### Automation

- Constraint engine: dependency, timebox, and custom constraint types
- Derived state: task status computed from constraints and events, never manually set
- Event-driven transitions: constraint evaluation triggers on every relevant event
- Signals integration: GitHub commits/PRs and Slack messages flow into the event log

### AI

- Natural-language command palette (NL to MCP tool resolution with confidence thresholds)
- Task relation detection (embedding similarity, accept/dismiss workflow)
- State inference from signals
- Priority suggestions
- Page/wiki generation from project and task context
- Weekly workspace digest
- Notification importance classification
- AI safety checks for public sharing (PII/secret detection)
- Cost guard with per-workspace budget limits

### Collaboration

- Server-Sent Events for real-time updates
- Notification system with in-app, email, and web push channels
- Per-user notification preferences with AI importance filtering
- Webhook subscriptions with HMAC-SHA256 signing and retry with exponential backoff
- Webhook delivery log and test-send UI
- Comments with Markdown support

### Organization

- Timeboxes (sprints/iterations) with date validation, burndown data, and auto status transitions
- Pages/Wiki with tree structure, Markdown, task linking (`[[TASK-{id}]]`), and AI generation
- Lenses: saved views with filter, sort, and group configuration
- CSV and JSON export with lens filter support and optional signals inclusion

### Sharing

- Public lens sharing with token-based read-only URLs
- AI safety check before publishing (PII and secret scanning)
- IP rate limiting on public endpoints

### Security

- 2FA TOTP enrollment, verification, and recovery codes
- Argon2id password hashing
- EdDSA JWT authentication with OIDC support
- AES-256-GCM encryption for secrets at rest
- Secret rotation CLI
- CSP nonce, HSTS, X-Content-Type-Options, X-Frame-Options
- Per-IP login rate limiting
- MIME type validation and file size limits on uploads
- Registration toggle (open/closed sign-up)
- ACL with four visibility layers
- Audit logging for authentication, permission changes, deletions, and AI invocations

### Observability

- OpenTelemetry tracing (HTTP, database, MCP, AI spans)
- Prometheus metrics (HTTP latency, DB pool, AI token usage)
- Four Grafana dashboards with provisioning and alerting
- Structured logging via `log/slog` with field redaction

### API and SDK

- 110+ RESTful endpoints via Huma (OpenAPI 3.1 generated from Go types)
- Scalar API reference UI
- TypeScript SDK generated from OpenAPI spec (`openapi-typescript` + `openapi-fetch`)
- Typed error codes generated from YAML single source into Go and TypeScript
- CLI (`tnk`) for task, project, and workspace operations

### MCP server

23 tools across domains:

- **Tasks:** create, update, search, list, archive
- **AI:** resolve NL command, suggest priority, infer state, detect relations
- **Timeboxes:** list, create, add task
- **Webhooks:** create, list, delete
- **Pages:** list, get, create, update, generate
- **Export:** export tasks (CSV/JSON)
- **Lenses:** propose lens

---

## Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.23+, Huma (chi router), sqlc, `database/sql` |
| Database | MySQL 9.6 |
| Frontend | TypeScript, React 19, Vite, Tailwind v4, TanStack (Router, Query, Table, Virtual) |
| State management | Zustand |
| Forms | React Hook Form + Zod |
| i18n | i18next with ICU message format |
| API boundary | OpenAPI 3.1 (auto-generated), TypeScript SDK |
| Auth | EdDSA JWT, OIDC, Argon2id, TOTP 2FA |
| Encryption | AES-256-GCM |
| Observability | OpenTelemetry, Prometheus, Grafana |
| Testing | testify, testcontainers-go, Vitest, Playwright |
| Linting | golangci-lint, Biome |

---

## Known limitations

These items are intentionally out of scope for v1.0 and planned for future
releases:

- **No mobile application.** PWA support is planned for v1.x.
- **No SSO SAML.** Enterprise SSO is planned for v2.0.
- **No offline mode.** Service Worker-based offline is under consideration.
- **No real-time collaborative editing.** CRDT-based concurrent editing (e.g., Y.js) is planned for a future release.
- **No plugin system.** An MCP-based extension mechanism is on the roadmap.
- **No multi-region deployment support.** Single-region, self-hosted deployment only for now.

---

## Deployment

nodate-flow is designed for self-hosted deployment via Docker Compose.

```sh
git clone https://github.com/libraz/nodate-flow
cd nodate-flow
cp .env.example .env    # edit with your secrets
docker compose up
```

The compose stack includes the Go API, React frontend, MySQL, Redis, MinIO
(S3-compatible object storage), Prometheus, and Grafana.

---

## Upgrading

This is the initial release. No migration from a previous version is needed.

Starting with v1.0, the project follows SemVer. Breaking changes will require
a major version bump and a migration guide.

---

## License

nodate-flow is licensed under [AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html).
You are free to self-host, modify, and redistribute. If you run a modified
version as a network service, AGPL section 13 requires you to make the
corresponding source available to its users.
