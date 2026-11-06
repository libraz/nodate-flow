# nodate-flow — unreleased

> **Draft.** Nothing has been tagged or released yet. These notes describe
> the current state of the main branch and will be finalised when the first
> release is cut.

nodate-flow is an open-source task platform that treats tasks as processes
rather than rows. Instead of clicking through status columns, task state is
derived from constraints and an event log. LLMs and MCP are not bolt-on
features but part of the execution layer itself.

---

## Highlights

### Constraint-driven task automation

Tasks in nodate-flow do not have a manually-assigned status. State is derived
from constraints (dependencies, timeboxes, assignee rules) and an append-only
event log. When a blocking task completes, the constraint engine evaluates all
dependents and transitions them automatically. A database trigger rejects
direct updates to the derived state, and every state change is auditable back
to the event that caused it. A completion policy can also require a submission
that a reviewer approves before the task completes.

### Signals and the judge loop

GitHub, Slack and Google webhooks, Discord presence, the arrival of a calendar
event's day and manually entered notes enter as signals. A deterministic
matcher decides whether a signal is worth a judge run, an LLM judge returns a
verdict without writing anything, and a deterministic applier turns the
verdict into events, a draft or a suggestion. Per-workspace autonomy
(`suggest`, `draft` or `auto`) decides how far a verdict goes on its own.

### AI assistance

A built-in AI layer turns natural language into action. Commands and queries
are compiled to tool calls and lenses, relation detection finds related
tasks, and background features infer task state, suggest priorities, triage
the inbox, draft pages and write a weekly digest. Providers are configured
per workspace: OpenAI, Anthropic, Google, Ollama or any OpenAI-compatible
endpoint, under a daily budget and request-rate limits.

### MCP server

nodate-flow serves a Model Context Protocol endpoint. Agents and external
tools authenticate with workspace tokens that carry scopes, and every call is
rate-limited and recorded. They operate on a workspace through the same ACL
and event log as human users.

### Views, calendar and real-time updates

Each project has board (kanban), list, dependency graph, spreadsheet,
timeline and Gantt views. A dashboard offers six widget types: task summary,
burndown chart, signals feed, AI suggestions, overdue tasks and notification
feed. A shared and personal calendar sits beside the task views, with
recurring events, attendees, invitations and reminders. Server-Sent Events
push updates to connected browsers, and HMAC-signed outbound webhooks let
external systems react to workspace events.

---

## Full feature list

### Core

- Workspaces with member management and role-based access (owner, admin, member, guest)
- Projects
- Tasks with title, description, priority, assignees and due dates
- Task dependencies and relations
- Review gates and handoffs
- Comments and activity timeline
- Lenses (saved filter/sort/group views)
- Dual-ID system: internal auto-increment for performance, public UUID v7 for API exposure

### Views

- Board (kanban) with drag-and-drop state transitions
- List with sortable/filterable columns
- Dependency graph visualization
- Spreadsheet with inline editing and bulk operations
- Timeline, and Gantt with dependencies and the critical path
- Dashboard with six widget types, drag-and-drop layout and resize

### Calendar

- Shared and personal calendars
- Recurring events, attendees with RSVP, invitations, reminders, checklists, comments and attachments
- Task links, and events created from tasks
- Public share and embed pages
- Public holidays per country

### Automation

- Constraint engine: dependencies and constraint rules decide when a task can start and finish
- Derived state: task status computed from constraints and events, never manually set
- Signals from GitHub, Slack, Google, Discord presence, calendar days and manual notes
- Judge loop with per-workspace autonomy rules

### AI

- Smart task creation from free text
- Natural-language commands and queries
- Task relation detection with accept/dismiss workflow
- State inference and priority suggestions
- Inbox triage, page drafting and a weekly workspace digest
- Notification importance classification
- AI safety checks for public sharing (PII/secret detection)
- Daily budget and request-rate limits

### Collaboration

- Server-Sent Events for real-time updates
- Notifications with snooze and per-user preferences
- Webhook subscriptions with HMAC signing and retry with exponential backoff

### Organization

- Timeboxes (sprints/iterations) with status lifecycle
- Pages with tree structure, Markdown and AI-assisted drafting
- CSV and JSON export with lens filter support, and CSV import
- Source intake that turns captured sources into tasks

### Sharing

- Public lens sharing with token-based read-only URLs
- AI safety check before publishing
- IP rate limiting on public endpoints

### Security

- 2FA TOTP enrollment, verification and recovery codes
- Argon2id password hashing
- EdDSA JWT authentication with OIDC sign-in (Google, GitHub, Microsoft) and an OIDC-only mode
- AES-256-GCM encryption for secrets at rest, with a secret rotation command
- CSP nonce, HSTS and X-Frame-Options
- Per-IP login rate limiting
- Registration toggle (open/closed sign-up)
- Audit logging

### Observability

- OpenTelemetry tracing and metrics
- Prometheus metrics (HTTP latency, DB pool, AI usage)
- Five Grafana dashboards with provisioning: HTTP, database, AI cost, business KPIs, worker and background jobs
- Structured logging via `log/slog` with field redaction

### API and SDK

- RESTful endpoints via Huma, with an OpenAPI 3.1 document generated from Go types
- Scalar API reference UI served by flow-api at `/api-reference`
- TypeScript SDK generated from the OpenAPI document (`openapi-typescript` + `openapi-fetch`)
- Typed error codes generated from a YAML single source into Go and TypeScript
- CLI (`tnk`)

---

## Stack

| Layer            | Technology                                                                        |
| ---------------- | --------------------------------------------------------------------------------- |
| Backend          | Go, Huma (chi router), sqlc, `database/sql`                                       |
| Database         | MySQL                                                                             |
| Frontend         | TypeScript, React 19, Vite, Tailwind v4, TanStack (Router, Query, Table, Virtual) |
| State management | Zustand                                                                           |
| Forms            | React Hook Form + Zod                                                             |
| i18n             | i18next with ICU message format                                                   |
| API boundary     | OpenAPI 3.1 (auto-generated), TypeScript SDK                                      |
| Auth             | EdDSA JWT, OIDC, Argon2id, TOTP 2FA                                               |
| Encryption       | AES-256-GCM                                                                       |
| Observability    | OpenTelemetry, Prometheus, Grafana                                                |
| Testing          | testify, testcontainers-go, Vitest, Playwright                                    |
| Linting          | golangci-lint, Biome                                                              |

---

## Known limitations

- Alpha: not independently security-audited.
- Only the CSV import connector runs. Jobs for other sources are accepted and finish as failed.
- Web push is not delivered; the preference flag exists without a sender.
- flow-api and flow-worker run as single instances by default. Moving the event stream and the rate limiter to a shared Redis-compatible store is the path to more replicas.
- No mobile application, SAML SSO, offline mode, real-time collaborative editing, plugin system or multi-region deployment.

---

## Deployment

nodate-flow is designed for self-hosted deployment via Docker Compose.
`compose.prod.yml` runs MySQL, object storage, auth-api, flow-api,
flow-worker and an nginx edge that serves both web apps. See the
[deployment guide](https://github.com/libraz/nodate-flow/blob/main/docs/en/deployment.md)
for the steps.

---

## Upgrading

No version has been released, so there is nothing to migrate from. Until the
first stable release no backward compatibility is kept, and the task schema is
rebuilt rather than migrated.

---

## License

nodate-flow is licensed under [AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html).
You are free to self-host, modify, and redistribute. If you run a modified
version as a network service, AGPL section 13 requires you to make the
corresponding source available to its users.
