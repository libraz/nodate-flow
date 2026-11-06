# Introduction

nodate-flow is a self-hosted task platform in which a task is a process rather than a row. A task's state follows from its dependencies, its constraints and an append-only log of events. LLMs and MCP are part of the execution path: they read the same events and write through the same rules as a person does.

## The name

"nodate" is a pun on "no date", work free of deadline pressure, and on the Japanese 野点 (nodate), preparing tea outdoors without hurry.

## How task state works

- **Constraints.** Dependencies between tasks and constraint rules decide when a task can start and finish.
- **Events.** Every change is a row in the `events` table, appended in the same transaction as the change. The actor is a user, an AI agent or a system source such as the worker.
- **Derived state.** A task's state is computed from its events. A database trigger refuses direct updates to `derived_state`, and a transition that the constraints reject is returned as an error rather than applied.
- **Review gates.** A completion policy can require a submission that a reviewer approves before the task completes. Handoffs are recorded on the task.

The interface follows from this. flow-web shows the flow instead of offering status buttons: board, list, dependency graph, spreadsheet, timeline and Gantt views per project, a dashboard of widgets, an activity feed, and an AI activity log. Lenses save a filtered view, and a lens can be shared read-only through a public link.

## Signals and the judge loop

External inputs enter as rows in the `signals` table. The sources are GitHub, Slack and Google webhooks, Discord presence, the arrival of a calendar event's day, and notes entered by hand. Each signal kind is defined in `signal_kinds/*.yaml`, which also sets its default autonomy.

A signal passes through three stages:

1. A deterministic matcher filters signals by workspace settings, kind and subject, and decides whether to start a judge run.
2. An LLM judge, run as an agent of kind `signal_judge`, reads the signal with workspace context and returns a verdict with a confidence. It does not write anything.
3. A deterministic applier turns the verdict into events, a draft or a suggestion.

Only the applier writes task events, so the event log stays the single writer of task state. Events written this way carry the judge agent as actor and point back to the signal that caused them. Autonomous completions and retro drafts are separate event kinds, which lets the UI label them and lets a user review drafts as a queue.

Autonomy is one of `suggest`, `draft` or `auto`, resolved per workspace and signal kind in a fixed order: a rule for the exact kind, a rule for a kind pattern such as `*.presence`, the workspace's default rule, the workspace confidence threshold, and finally the default in the kind's YAML. The signal retention job in flow-worker removes expired signals that were never applied.

Discord presence is the one source that needs a gateway process. Each operator runs their own bot in their own Discord guild, and users link their Discord account in their integration settings. See [Architecture](architecture.md) for how the gateway connects.

## AI and MCP

Every LLM call goes through one invocation path, so cost limits, invocation logs and per-workspace provider choice apply to all of them. Providers are configured per workspace: OpenAI, Anthropic, Google, Ollama, or any OpenAI-compatible endpoint. A daily budget and request-rate limits cap spending. AI features need a provider configured in the workspace settings.

The AI features are:

- smart task creation from free text,
- state inference and priority suggestions,
- relation suggestions between tasks,
- natural-language commands and queries that compile to lenses,
- inbox triage and a weekly digest,
- page drafting.

Suggestions are shown with accept, edit and dismiss actions. Per-workspace auto-action rules decide which ones may apply on their own.

flow-api also serves an MCP endpoint. Agents and external tools authenticate with workspace tokens that carry scopes, and every call is rate-limited and recorded. External services therefore act inside the workspace through the same ACL and event log as the UI, instead of through separate integration code.

## Calendar

flow-web includes a calendar served by flow-api. It has shared and personal calendars, recurring events, attendees with RSVP, invitations, reminders, checklists, comments, attachments, and public share and embed pages. A task can link to calendar events and can create one, and public holidays are available per country. The calendar needs no other product.

## nodate-time

[nodate-time](https://github.com/libraz/nodate-time) is a separate calendar product with its own repository, backend and deployment. Neither product depends on the other's code.

Both can optionally point at the same MySQL database. They then share users, groups and calendar data through a published schema contract: the core layer in `sql/core/` and the protocol in `sql/core/PROTOCOL.md`. [Architecture](architecture.md) describes the layers and what each product owes the contract.

## Compared with row-and-status task tools

Most task tools store a task as a row whose status a person updates. That keeps the model simple and every change direct, and it leaves the reason for a status change outside the system.

Here state is derived from constraints and an event log, LLMs act as recorded actors, and external services arrive as signals instead of bespoke integrations. The cost is that status cannot be set freely: intent is expressed through transitions, constraints and events, and several invariants live in the database, where they are harder to debug than application code.

## Status and limits

- The project is alpha. No backward compatibility is kept before the first stable release. The core and time schema layers ship upgrades that keep data, and the flow layer is rebuilt instead of migrated.
- It has not been independently security-audited. Vulnerability reports go through `SECURITY.md`.
- Only the CSV import connector runs. Jobs for other sources are accepted and finish as failed.
- flow-api and flow-worker are designed to run as single instances by default. Moving the event stream and the rate limiter to a shared Redis-compatible store is the path to more replicas.
- The license is AGPL-3.0. Offering a modified version over a network requires making its source available.
