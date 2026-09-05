package mcp

import (
	"context"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// Every REST mutation leaves two traces: a row in `events`, which drives
// the timeline, notifications, webhooks and the live SSE feeds, and a
// row in `audit_logs`, which is what an administrator queries by action
// name when they need to know who did what. The MCP tools left one, the
// other, or neither depending on which tool it was — an agent's
// calendar entry appeared in nobody's timeline, an agent's import
// bypassed the guard that stops two imports running at once, and an
// agent's bulk task export left no trace at all on the surface agents
// use most.
//
// Adding the two calls to the three tools named in the audit would fix
// those three tools. The fourth tool written after this comment would
// open the same hole, which is the failure mode this repository already
// refuses elsewhere: internal/taskcreate is the only way to build a
// task, internal/ai/airequest the only way to build a provider request,
// and in both cases a static test — not a convention — is what keeps
// the second path from appearing.
//
// So the pair is one function, and mutationlog.go is the only file in
// this package allowed to call eventbus.Append*. mutation_static_test.go
// enforces both halves off the go/ast call graph: a write-scoped tool
// that never reaches recordMutation fails the build, and so does a tool
// that reaches around it to the eventbus directly.

// mutation is one workspace-visible change made through an MCP tool.
//
// EventType and AuditAction are both required; a mutation that names
// only one of them records only half the change, which is the exact
// state this type exists to make impossible. The static test rejects a
// literal that omits either, so the runtime guard below only ever fires
// for a value assembled dynamically.
type mutation struct {
	// EventType is the canonical eventbus kind appended to `events`,
	// e.g. eventbus.CalEventCreated. Use the same kind the REST handler
	// for this operation uses — a second spelling of the same change
	// splits every consumer that subscribes by kind.
	EventType eventbus.Kind
	// AuditAction is the audit_logs.action, e.g. "calendar.event.create".
	// Same rule: match REST, because the administrator querying it does
	// not know or care which transport made the change.
	AuditAction string
	// ResourceType names the kind of row affected ("calendar.event",
	// "import_job", "export"), matching the REST handler's value.
	ResourceType string
	// ResourceID is the public UUID of the affected row. Empty is
	// allowed for operations that affect no single row (an export).
	ResourceID string
	// TaskID is the internal task id when the change targets a task, so
	// events.task_id carries the link the timeline reads.
	TaskID *int64
	// CalendarID is the internal calendar id when the change happened
	// inside a calendar, so events.calendar_id carries the link a
	// per-calendar activity feed reads. Same field the REST calendar
	// handlers fill; without it a calendar.* mutation says what happened
	// but not where, and that feed cannot see it at all. Nil for changes
	// with no calendar subject.
	CalendarID *int64
	// Payload is the event payload and doubles as the audit metadata. It
	// must already be free of secrets and internal sequential ids.
	Payload map[string]any
	// CallSite names the operation ("mcp.create_calendar_event") for the
	// log line written if the event row is lost.
	CallSite string
}

// recordMutation writes the event row and the audit row for a change
// that is already committed.
//
// It never fails the caller. Use it where the client cannot safely
// retry — the task exists, the event exists, the export already left
// the building — because returning an error there would report that
// nothing happened for work that did, and the retry would duplicate it.
// Where the mutation is idempotent, use [recordMutationStrict] and
// propagate: the retry is what repairs the log.
func recordMutation(ctx context.Context, deps Deps, s *session, m mutation) {
	if !mutationIsComplete(ctx, m) {
		return
	}
	eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), mutationEvent(s, m), m.CallSite)
	recordMutationAudit(ctx, deps, s, m)
}

// recordMutationStrict is [recordMutation] for an idempotent change: the
// event row is required, and a failure to write it is the caller's
// error. The audit row stays best-effort either way — losing it must not
// turn a successful mutation into a failure the client retries.
func recordMutationStrict(ctx context.Context, deps Deps, s *session, m mutation) error {
	if !mutationIsComplete(ctx, m) {
		return nil
	}
	if err := eventbus.Append(ctx, dbretry.AutoCommit(deps.DB), mutationEvent(s, m)); err != nil {
		return err
	}
	recordMutationAudit(ctx, deps, s, m)
	return nil
}

// recordTxMutationAudit records the audit half of a change whose event
// row was appended by a shared transactional helper — taskstate for a
// task transition, itemkit for a calendar edit or delete.
//
// Those helpers own the event on purpose: appending it inside the same
// transaction as the state change is what makes the pair atomic, and
// re-appending here would put the change on the timeline twice. What
// they cannot supply is the audit row, because they are shared with REST
// and know nothing about which transport called them. So this is the one
// place a mutation may leave EventType empty, and the call site has to
// name the helper that already emitted it.
func recordTxMutationAudit(ctx context.Context, deps Deps, s *session, m mutation) {
	if m.AuditAction == "" {
		slog.ErrorContext(ctx, "mcp: mutation audit record without an action dropped",
			slog.String("call_site", m.CallSite),
		)
		return
	}
	recordMutationAudit(ctx, deps, s, m)
}

// mutationIsComplete guards the dynamic case the static test cannot see.
// A half-described mutation is dropped rather than half-recorded: a row
// in one table and not the other is worse than none, because it reads as
// a complete answer to whoever queries that table.
func mutationIsComplete(ctx context.Context, m mutation) bool {
	if m.EventType != "" && m.AuditAction != "" {
		return true
	}
	slog.ErrorContext(ctx, "mcp: incomplete mutation record dropped",
		slog.String("event_type", string(m.EventType)),
		slog.String("audit_action", m.AuditAction),
		slog.String("call_site", m.CallSite),
	)
	return false
}

func mutationEvent(s *session, m mutation) eventbus.Event {
	actor := int64(s.userID)
	return eventbus.Event{
		Type:        m.EventType,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      m.TaskID,
		CalendarID:  m.CalendarID,
		Payload:     m.Payload,
	}
}

// recordMutationAudit appends the audit_logs row. The recorder is built
// per call because it is a stateless wrapper around the sqlc handle the
// tools already carry, which keeps the MCP handler's dependency bundle —
// and the router wiring that fills it — unchanged.
func recordMutationAudit(ctx context.Context, deps Deps, s *session, m mutation) {
	if deps.Queries == nil {
		return
	}
	audit.New(deps.Queries).Record(ctx, audit.Entry{
		Action:       m.AuditAction,
		ActorID:      s.userID,
		WorkspaceID:  s.workspaceID,
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
		Metadata:     m.Payload,
	})
}
