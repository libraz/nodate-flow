package signaljudge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// EnqueuerDB is the narrow surface the enqueuer needs from the
// database handle. *sql.DB satisfies it; tests can pass a thin fake.
//
// We deliberately do not depend on the sqlc Queries bundle here: the
// match query mixes JSON_CONTAINS with NULL/empty fallbacks against
// the ai_agents.event_trigger_types column, which is awkward to
// express in a single sqlc :many statement that also reads cleanly
// from review. Raw SQL keeps the predicate visible inline.
type EnqueuerDB interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// Enqueuer matches a newly inserted signal against the workspace's
// signal_judge agents and enqueues one agent_runs row per match
// through the supplied agentruntime.Queue. Duplicate enqueues are
// swallowed via [agentruntime.ErrDuplicate] so replaying the same
// signal across replicas is safe.
//
// The Enqueuer is intentionally side-effect-only on failure: it logs
// and returns, but never blocks the calling signal-ingestion path.
// Signal insertion is the canonical write; failing to wake the judge
// must not also fail the user-visible signal POST.
type Enqueuer struct {
	DB     EnqueuerDB
	Queue  agentruntime.Queue
	Logger *slog.Logger
	Now    func() time.Time
	// Matcher is the optional deterministic pre-judge gate.
	// When non-nil, every EnqueueForSignal call runs
	// the matcher first; signals the matcher rejects are not
	// enqueued (and the agent_runs slot is not consumed). nil
	// disables the gate — useful for tests that already curate the
	// signal set they want judged.
	Matcher *Matcher
	// SubjectType / SubjectID enrich the matcher call when the
	// signal is subject-scoped. Pass these from the ingestion path
	// (handlers.go / webhooks_*.go) when calling EnqueueForSignal so
	// the matcher can verify the subject exists. Both default to
	// empty / NULL when not supplied; matcher then skips the subject
	// existence filter for that signal.
	//
	// Kept on the call signature (not the struct) via the
	// EnqueueForSignalMatched helper below so EnqueueForSignal stays
	// backward-compatible for callers that haven't migrated yet.
}

// matchingSignalJudgesQuery selects every enabled, unpaused
// signal_judge agent in the workspace whose event_trigger_types JSON
// array either contains the signal's kind or is empty / NULL
// (interpreted as a wildcard subscription to every signal kind).
//
// The empty-array branch uses JSON_LENGTH = 0 so that a deliberate
// `[]` written by the admin UI is treated the same as a NULL column
// — both mean "subscribe to every kind".
const matchingSignalJudgesQuery = `SELECT a.id
FROM ai_agents a
WHERE a.workspace_id = ?
  AND a.enabled = TRUE
  AND a.paused = FALSE
  AND a.kind = 'signal_judge'
  AND (
    a.event_trigger_types IS NULL
    OR JSON_LENGTH(a.event_trigger_types) = 0
    OR JSON_CONTAINS(a.event_trigger_types, JSON_QUOTE(?))
  )`

// EnqueueForSignal looks up matching signal_judge agents for the
// freshly inserted signal and enqueues one agent_runs row per match.
//
// The dedupe_key is shaped as `judge:<agentID>:<signalID>` so that
// re-running the same enqueue (e.g. retried webhook delivery, replayed
// notify hook) collapses to a single queue row across scheduler
// replicas. signalID = 0 (duplicate INSERT IGNORE on the signal row)
// short-circuits without enqueueing — no judge run is meaningful for
// a row that did not actually land.
//
// Errors from individual enqueues are logged and swallowed; the
// function only returns a non-nil error when the match query itself
// fails. This matches the existing on_event trigger semantics, where
// a flaky agent lookup must not block the eventbus append.
func (e *Enqueuer) EnqueueForSignal(ctx context.Context, signalID int64, workspaceID uint32, kind signalkinds.Kind) error {
	return e.EnqueueForSignalMatched(ctx, signalID, workspaceID, kind, "", sql.NullInt32{})
}

// EnqueueForSignalMatched is the matcher-aware variant of
// [EnqueueForSignal]. It runs the per-(workspace, kind, subject)
// matcher gate before scanning the agent table; signals the matcher
// rejects are logged at debug and not enqueued.
//
// Callers that have not yet been ported to pass subject info can keep
// using EnqueueForSignal; that path runs the matcher with an empty
// subject and only the workspace / kind / dedupe filters are applied.
func (e *Enqueuer) EnqueueForSignalMatched(ctx context.Context, signalID int64, workspaceID uint32, kind signalkinds.Kind, subjectType string, subjectID sql.NullInt32) error {
	if e == nil || e.DB == nil || e.Queue == nil {
		return nil
	}
	if signalID <= 0 {
		// Duplicate INSERT IGNORE returns 0 from LastInsertId; the
		// signal already exists and a judge run was either enqueued at
		// first insert or intentionally skipped. Either way, do nothing.
		return nil
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	logger := e.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Matcher pre-gate. Reject early so we do not spend an
	// agent_runs row on a signal the matcher knows the judge would
	// just skip. Errors from the matcher are logged but treated as
	// "let it through" — the judge has its own filters and we would
	// rather over-enqueue than silently drop a signal because of a
	// transient lookup failure.
	if e.Matcher != nil {
		decision, reason, mErr := e.Matcher.Match(ctx, signalID, workspaceID, kind, subjectType, subjectID)
		if mErr != nil {
			logger.WarnContext(ctx, "signaljudge: matcher errored, proceeding without gate",
				slog.Any("err", mErr),
				slog.String("kind", string(kind)),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
				slog.Int64("signal_internal", signalID),
			)
		} else if decision != Eligible {
			logger.DebugContext(ctx, "signaljudge: matcher skipped signal",
				slog.String("decision", string(decision)),
				slog.String("reason", reason),
				slog.String("kind", string(kind)),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
				slog.Int64("signal_internal", signalID),
			)
			return nil
		}
	}

	rows, err := e.DB.QueryContext(ctx, matchingSignalJudgesQuery, workspaceID, string(kind))
	if err != nil {
		return fmt.Errorf("signaljudge: match query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	now := e.Now().UTC()
	for rows.Next() {
		var agentID uint32
		if scanErr := rows.Scan(&agentID); scanErr != nil {
			logger.WarnContext(ctx, "signaljudge: match scan failed",
				slog.Any("err", scanErr),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
				slog.String("kind", string(kind)),
			)
			continue
		}
		// Dedupe key shape mirrors the existing event-trigger key
		// convention (<agentID>:<scope>:<id>) so operators can grep
		// agent_runs.dedupe_key for `judge:` and find every judge run
		// regardless of which agent matched. The signal id is the
		// natural per-run identifier since one signal = one judge run.
		dedupeKey := DedupeKeyForSignal(agentID, signalID)
		run := agentruntime.Run{
			DedupeKey:   dedupeKey,
			Job:         agentruntime.Job{AgentID: agentID, WsID: workspaceID, DedupeKey: dedupeKey},
			ScheduledAt: now,
		}
		if enqErr := e.Queue.Enqueue(ctx, run); enqErr != nil && !errors.Is(enqErr, agentruntime.ErrDuplicate) {
			logger.WarnContext(ctx, "signaljudge: enqueue failed",
				slog.Any("err", enqErr),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
				slog.Uint64("agent_internal", uint64(agentID)),
				slog.Int64("signal_internal", signalID),
				slog.String("kind", string(kind)),
			)
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("signaljudge: match rows: %w", rowsErr)
	}
	return nil
}

// DedupeKeyForSignal returns the canonical dedupe_key shape used by
// EnqueueForSignal. Exposed so the runner (which reads the agent_runs
// row back via SignalIDFromDedupeKey) can express the parse logic in
// one place. Keep these two functions in sync.
func DedupeKeyForSignal(agentID uint32, signalID int64) string {
	return fmt.Sprintf("judge:%d:%d", agentID, signalID)
}

// SignalIDFromDedupeKey parses a judge dedupe_key produced by
// DedupeKeyForSignal and returns the signal id. ok = false when the
// key was not shaped by this package (which means the caller should
// fall back to the task-agent path).
//
// We use a fixed-shape parser instead of a regex to match the
// project's "no regex" convention. The dedupe_key column is latin1 so
// strings.Split is safe.
func SignalIDFromDedupeKey(dedupeKey string) (signalID int64, ok bool) {
	parts := strings.Split(dedupeKey, ":")
	if len(parts) != 3 || parts[0] != "judge" {
		return 0, false
	}
	if agentID, err := strconv.ParseUint(parts[1], 10, 32); err != nil || agentID == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
