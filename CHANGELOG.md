# Changelog

## Unreleased

### Features

- **Task management**: Full lifecycle with derived state, constraints and dependencies; board, list, dependency graph, spreadsheet, timeline and Gantt views per project
- **Review gates**: Completion policies that require a submission a reviewer approves, with handoffs recorded on the task
- **Projects and workspaces**: Multi-tenant workspace isolation with role-based access control
- **Signals and judge loop**: GitHub, Slack and Google webhooks, Discord presence, calendar event days and manual notes become signals; an LLM judge proposes task events and per-workspace autonomy decides whether to suggest, draft or apply
- **AI assistance**: Natural-language commands, relation detection, state inference, priority suggestions, smart task creation, inbox triage, weekly digest
- **Calendar**: Shared and personal calendars with recurring events, attendees, invitations, reminders, task links, and public share and embed pages
- **Timeboxes**: Time-bounded work containers with status lifecycle
- **Pages/Wiki**: Tree-structured documentation with Markdown editing and AI generation
- **Dashboard**: Configurable widget grid with six widget types
- **Notifications**: Event-driven notification system with AI importance filtering
- **Webhooks**: HMAC-signed delivery with exponential backoff retry
- **Export and import**: CSV and JSON export with lens filter support, and CSV import
- **Public sharing**: Token-based read-only lens sharing with AI safety checks
- **MCP server**: Agents and external tools read and write a workspace through scoped tokens
- **Real-time**: SSE-based live updates
- **Security**: 2FA TOTP with recovery codes, CSP/HSTS, rate limiting, audit logging
- **Observability**: OpenTelemetry, Prometheus metrics, Grafana dashboards
- **API reference**: Scalar UI generated from the OpenAPI document
