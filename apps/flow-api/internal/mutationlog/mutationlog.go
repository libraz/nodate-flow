// Package mutationlog records one workspace-visible change in the two
// places such a change has to appear: a row in `events`, which drives
// the timeline, notifications, webhooks and the live SSE feeds, and a
// row in `audit_logs`, which is what an administrator queries by action
// name when they need to know who did what.
//
// The two rows answer different questions and neither substitutes for
// the other, so a change that reaches only one table is not "mostly
// recorded" — it is a complete answer to whoever queries that table and
// a silence to whoever queries the other. Across the HTTP handlers both
// halves of that failure are present today: calendar membership,
// checklist, comment and attachment changes append an event and leave no
// audit row, while agent handoffs, timebox deletion and project CRUD
// write the audit row and appear on no timeline.
//
// Adding the missing call at each of those sites would fix those sites.
// The site written next would open the same hole again, which is the
// failure mode this repository already refuses elsewhere:
// internal/taskcreate is the only way to build a task, internal/airequest
// the only way to build a provider request, and in both cases a static
// test — not a convention — is what keeps a second path from appearing.
//
// So the pair is one function, this package owns it, and a package that
// routes its mutations through here declares that with a guard built on
// mutationguard: within such a package no file may reach past the
// recorder to eventbus.Append* or to the audit recorder, and every
// non-GET operation must reach a recorder entry point.
//
// The transports share the recorder because the administrator querying
// audit_logs, and the subscriber reading the event stream, do not know
// or care whether a change arrived over HTTP or over MCP. The package
// therefore names no request type, no session type and no handler
// dependency bundle; a caller supplies an [Actor] and a [Mutation].
package mutationlog

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// Actor is who made the change and where. Both tables record the same
// pair, so it is one value rather than two arguments each call site
// could get out of step.
//
// UserID zero means there is no authenticated user behind the change —
// an unauthenticated magic-link accept, a webhook delivery. Both rows
// then carry a NULL actor rather than a fabricated one.
type Actor struct {
	// UserID is the internal users.id of the actor, or zero for none.
	UserID uint32
	// WorkspaceID is the internal workspaces.id the change belongs to.
	// Required: a row in either table without it belongs to no tenant.
	WorkspaceID uint32
}

// Mutation is one workspace-visible change.
//
// EventType and AuditAction are both required. A mutation naming only
// one of them records only half the change, which is the exact state
// this type exists to make impossible; the static guard rejects a
// literal that omits either, so the runtime check below only ever fires
// for a value assembled dynamically.
type Mutation struct {
	// EventType is the canonical eventbus kind appended to `events`,
	// e.g. eventbus.IntakeItemCreated. Every transport that performs
	// this operation uses the same kind — a second spelling of one
	// change splits every consumer that subscribes by kind.
	EventType eventbus.Kind
	// AuditAction is the audit_logs.action, e.g. "intake.create". Same
	// rule: one operation, one action name, whatever the transport.
	AuditAction string
	// ResourceType names the kind of row affected ("intake_item",
	// "task", "calendar.event").
	ResourceType string
	// ResourceID is the public UUID of the affected row. Empty is
	// allowed for an operation that affects no single row.
	ResourceID string
	// TaskID is the internal task id when the change targets a task, so
	// events.task_id carries the link the task timeline reads.
	TaskID *int64
	// CalendarID is the internal calendar id when the change happened
	// inside a calendar, so events.calendar_id carries the link a
	// per-calendar activity feed reads. Nil for changes with no
	// calendar subject.
	CalendarID *int64
	// Payload describes what changed. It is stored as both the event
	// payload and the audit metadata, deliberately: two descriptions of
	// one change drift, and a reader comparing the tables then cannot
	// tell which one is stale. Where the two used to differ the union
	// of the pair is the right value, because each was a strict subset
	// of what the change actually was.
	//
	// It must already be free of secrets and of internal sequential
	// ids. The event append enforces the second rule on the way in, so
	// routing the audit metadata through the same value extends that
	// rail to a table that never had one.
	Payload map[string]any
	// CallSite names the operation ("intake.Create") in the log line
	// written if the event row is lost.
	CallSite string
}

// Recorder writes both halves. A nil *Recorder is not silently
// tolerated: dropping the record of a change is the condition this
// package exists to prevent, so an unwired recorder says so in the log
// rather than passing for a healthy request.
type Recorder struct {
	db *sql.DB
	// audit is nil when the recorder was built without a query handle,
	// which the [audit.Recorder] nil receiver already treats as a no-op.
	audit *audit.Recorder
}

// New builds a Recorder over the pooled handle used for the event row
// and the sqlc handle used for the audit row.
func New(db *sql.DB, q *generated.Queries) *Recorder {
	r := &Recorder{db: db}
	if q != nil {
		r.audit = audit.New(q)
	}
	return r
}

// Record writes both rows for a change that is already durable.
//
// It never fails the caller, and that is right for an HTTP handler for
// the same reason it is right for an MCP tool: by the time it runs, the
// write it describes has committed on its own connection. Returning an
// error here would answer a request that changed something with a
// status saying nothing happened, and the client's retry would perform
// the change a second time. The handler has a response to shape, but
// shaping it around a lost log entry means lying about the mutation.
//
// Use [Recorder.RecordInTx] where the change has not committed yet, and
// [Recorder.RecordStrict] where the change is idempotent and a retry is
// what repairs the log.
func (r *Recorder) Record(ctx context.Context, a Actor, m Mutation) {
	if !r.ready(ctx, a, m, true) {
		return
	}
	eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(r.db), r.event(a, m), m.CallSite)
	r.writeAudit(ctx, a, m)
}

// RecordStrict is [Recorder.Record] for an idempotent change: the event
// row is required and a failure to write it is the caller's error, so
// the retry the client makes repairs the log instead of duplicating the
// change. The audit row stays best effort either way — losing it must
// not turn a successful mutation into a failure.
func (r *Recorder) RecordStrict(ctx context.Context, a Actor, m Mutation) error {
	if !r.ready(ctx, a, m, true) {
		return nil
	}
	if err := eventbus.Append(ctx, dbretry.AutoCommit(r.db), r.event(a, m)); err != nil {
		return err
	}
	r.writeAudit(ctx, a, m)
	return nil
}

// RecordInTx writes both rows for a change that has not committed yet.
//
// The event joins tx, so the change and the row describing it commit or
// roll back together — losing an event a projection reads is a wrong
// state nothing later corrects, and dbretry refuses to commit a
// transaction that lost one whatever this call site does with the error.
//
// The audit row is deliberately not written inline. It goes on the
// transaction's after-commit hook, so a transaction that rolls back
// leaves no audit entry claiming a change that did not happen, and the
// INSERT does not run on a second connection while this transaction
// still holds locks. It is registered rather than returned because a
// failed audit write must not take the mutation down with it.
//
// The parameter is the concrete transaction rather than a commit
// boundary on purpose: on the auto-commit path there is nothing to
// defer, and passing that handle here would silently degrade to
// [Recorder.RecordStrict] while reading as if it were transactional.
func (r *Recorder) RecordInTx(ctx context.Context, tx *dbretry.Tx, a Actor, m Mutation) error {
	if !r.ready(ctx, a, m, true) {
		return nil
	}
	if err := eventbus.Append(ctx, tx, r.event(a, m)); err != nil {
		return err
	}
	tx.AfterCommit(func() { r.writeAudit(ctx, a, m) })
	return nil
}

// RecordTxAudit records the audit half of a change whose event row was
// appended by a shared transactional helper — taskstate for a task
// transition, taskdeps for a dependency edge, itemkit for a calendar
// edit or delete.
//
// Those helpers own the event on purpose: appending it inside the same
// transaction as the state change is what makes the pair atomic, and
// appending it again here would put the change on the timeline twice.
// What they cannot supply is the audit row, because they are shared
// across transports and know nothing about which one called them. So
// this is the one entry point a mutation may reach with EventType
// empty, and the call site has to name the helper that emitted it.
//
// Call it after the helper's transaction has committed. Before that, an
// audit row can outlive a rollback.
func (r *Recorder) RecordTxAudit(ctx context.Context, a Actor, m Mutation) {
	if !r.ready(ctx, a, m, false) {
		return
	}
	r.writeAudit(ctx, a, m)
}

// ready reports whether the recorder can write and the mutation says
// enough to be worth writing. A half-described mutation is dropped
// rather than half-recorded: a row in one table and not the other reads
// as a complete answer to whoever queries that table.
//
// needEvent is false at the one entry point whose event another writer
// owns.
func (r *Recorder) ready(ctx context.Context, a Actor, m Mutation, needEvent bool) bool {
	if r == nil {
		slog.ErrorContext(ctx, "mutationlog: change dropped, no recorder wired",
			slog.String("audit_action", m.AuditAction),
			slog.String("call_site", m.CallSite),
		)
		return false
	}
	if a.WorkspaceID == 0 {
		slog.ErrorContext(ctx, "mutationlog: change dropped, no workspace",
			slog.String("audit_action", m.AuditAction),
			slog.String("call_site", m.CallSite),
		)
		return false
	}
	if m.AuditAction == "" || (needEvent && m.EventType == "") {
		slog.ErrorContext(ctx, "mutationlog: incomplete change dropped",
			slog.String("event_type", string(m.EventType)),
			slog.String("audit_action", m.AuditAction),
			slog.String("call_site", m.CallSite),
		)
		return false
	}
	return true
}

// event builds the row appended to `events`.
func (r *Recorder) event(a Actor, m Mutation) eventbus.Event {
	var actor *int64
	if a.UserID != 0 {
		v := int64(a.UserID)
		actor = &v
	}
	return eventbus.Event{
		Type:        m.EventType,
		WorkspaceID: a.WorkspaceID,
		ActorUserID: actor,
		TaskID:      m.TaskID,
		CalendarID:  m.CalendarID,
		Payload:     m.Payload,
	}
}

// writeAudit appends the audit_logs row. Failures are logged and
// counted by the audit recorder rather than returned: an audit backend
// problem must not turn a change that happened into a request that
// failed.
func (r *Recorder) writeAudit(ctx context.Context, a Actor, m Mutation) {
	r.audit.Record(ctx, audit.Entry{
		Action:       m.AuditAction,
		ActorID:      a.UserID,
		WorkspaceID:  a.WorkspaceID,
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
		Metadata:     m.Payload,
	})
}
