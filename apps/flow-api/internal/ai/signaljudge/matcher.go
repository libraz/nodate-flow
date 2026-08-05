// Package signaljudge — deterministic pre-judge eligibility filter
// (ADR 0008 D4 / Phase 3 J4).
//
// The Matcher runs synchronously inside the signal-ingestion path
// (apps/flow-api/internal/http/handlers/signals/handlers.go) before
// the Enqueuer queues a judge run. Pure Go, no LLM, no event
// emissions — its only job is to drop signals that are guaranteed to
// be uninteresting to the judge so the agent_runs queue does not
// burn slots on them.
//
// Skip reasons (any of these short-circuits before enqueue):
//   - The workspace has disabled the AI loop (ai_settings.auto_action_enabled = FALSE).
//   - The signal's subject row does not exist or has been disabled.
//   - A more-recent same-(workspace, kind, subject) signal is already
//     within the per-kind dedupe window for stateful kinds (presence,
//     weather). Discrete kinds bypass the dedupe gate because every
//     row is an independent event.
//
// SignalRejected events are NOT emitted from the Matcher — that kind
// is reserved for verdicts the judge dropped after running. A
// matcher skip just logs at debug and short-circuits.
package signaljudge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/signalkinds"
)

// MatcherDecision is the enumerated outcome of a matcher check.
type MatcherDecision string

// Matcher decision values.
const (
	// Eligible means the signal passed every filter and the caller
	// should proceed to enqueue a judge run.
	Eligible MatcherDecision = "eligible"
	// SkippedDedupe means the per-kind dedupe window already covers
	// a recent same-(workspace, kind, subject) signal. The caller
	// must not enqueue.
	SkippedDedupe MatcherDecision = "skipped_dedupe"
	// SkippedSubjectMissing means the signal references a subject
	// (task / user / calendar event) that does not exist or has been
	// disabled inside the workspace.
	SkippedSubjectMissing MatcherDecision = "skipped_subject_missing"
	// SkippedDisabled means the workspace's AI loop is paused
	// (ai_settings.auto_action_enabled = FALSE). Mirrors the gate
	// the auto-action executor already honours.
	SkippedDisabled MatcherDecision = "skipped_disabled"
	// SkippedUnknownKind means the signal kind is not registered in
	// signalkinds. Should be unreachable in practice because the
	// ingestion handler already rejects unknown kinds, but we still
	// short-circuit defensively here so a future kind registry drift
	// does not surface as an obscure judge crash.
	SkippedUnknownKind MatcherDecision = "skipped_unknown_kind"
)

// defaultStatefulDedupeWindow caps how often we re-judge the same
// (workspace, stateful_kind, subject) — a presence transition twice
// per minute is meaningful, a hundred times per minute is noise.
// Stateful kinds (presence, weather) use this; discrete kinds skip
// the gate.
const defaultStatefulDedupeWindow = 60 * time.Second

// MatcherDB is the narrow surface the Matcher needs from the database
// handle. *sql.DB satisfies it; tests pass a fake. Kept tight so the
// Matcher does not implicitly grow direct-SQL surface area.
type MatcherDB interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// Matcher is the deterministic pre-judge filter constructed once at
// startup and called from the signal-ingestion path. All knobs are
// optional — defaults mirror the values committed in the YAML signal
// kind registry.
type Matcher struct {
	// DB is the read handle used for AI settings, subject lookups,
	// and dedupe scans.
	DB MatcherDB
	// Logger is used for debug-level skip diagnostics. nil falls back
	// to slog.Default(), so production wiring can leave it unset.
	Logger *slog.Logger
	// Now is injected so tests can pin "is this signal within the
	// dedupe window" decisions deterministically. nil falls back to
	// time.Now.
	Now func() time.Time
	// StatefulDedupeWindow overrides the default per-kind dedupe
	// window for stateful kinds. Zero (or unset) falls back to
	// [defaultStatefulDedupeWindow].
	StatefulDedupeWindow time.Duration
}

// Match decides whether the signal at (workspaceID, signalID, kind)
// is eligible for a judge run. Returns the decision plus a
// human-readable reason for the debug log; on internal error it
// returns SkippedSubjectMissing-like decision and the wrapping error
// — callers should treat any non-nil error as "do not enqueue, log
// at warn".
func (m *Matcher) Match(ctx context.Context, signalID int64, workspaceID uint32, kind signalkinds.Kind, subjectType string, subjectID sql.NullInt32) (MatcherDecision, string, error) {
	if m == nil || m.DB == nil {
		// Matcher is optional — when no DB is wired we cannot enforce
		// the gate, so we default to "eligible" rather than swallow
		// every signal. The Enqueuer is the next gate downstream and
		// will still respect agent / workspace settings via its own
		// query.
		return Eligible, "matcher not configured", nil
	}
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	window := m.StatefulDedupeWindow
	if window == 0 {
		window = defaultStatefulDedupeWindow
	}

	// Filter 1 — kind registry. Should be redundant with the ingestion
	// handler's validation, but cheaper than a DB roundtrip so we
	// check it first.
	def, known := signalkinds.Lookup(kind)
	if !known {
		logger.DebugContext(ctx, "signaljudge.matcher: skip unknown kind",
			slog.String("kind", string(kind)),
			slog.Uint64("workspace_internal", uint64(workspaceID)),
			slog.Int64("signal_internal", signalID),
		)
		return SkippedUnknownKind, fmt.Sprintf("kind %q not registered", kind), nil
	}

	// Filter 2 — workspace AI loop kill switch. ai_settings.auto_action_enabled
	// is the shared toggle the existing auto-action executor honours;
	// the judge participates in the same on/off bit so admins have
	// one place to disable AI for a workspace. A missing settings
	// row is treated as "enabled" because the column default is TRUE.
	enabled, err := m.aiEnabled(ctx, workspaceID)
	if err != nil {
		return SkippedDisabled, "ai_settings load failed", fmt.Errorf("signaljudge.matcher: ai_settings load: %w", err)
	}
	if !enabled {
		logger.DebugContext(ctx, "signaljudge.matcher: skip ai disabled",
			slog.String("kind", string(kind)),
			slog.Uint64("workspace_internal", uint64(workspaceID)),
			slog.Int64("signal_internal", signalID),
		)
		return SkippedDisabled, "ai_settings.auto_action_enabled = false", nil
	}

	// Filter 3 — subject existence. Workspace subjects are owned by
	// the workspace_id column itself so they need no extra lookup;
	// every other subject type points at a row that must exist and
	// be enabled inside the workspace.
	if subjectID.Valid {
		exists, sErr := m.subjectExists(ctx, workspaceID, subjectType, subjectID.Int32)
		if sErr != nil {
			return SkippedSubjectMissing, "subject lookup failed", fmt.Errorf("signaljudge.matcher: subject lookup: %w", sErr)
		}
		if !exists {
			logger.DebugContext(ctx, "signaljudge.matcher: skip subject missing",
				slog.String("kind", string(kind)),
				slog.String("subject_type", subjectType),
				slog.Int("subject_internal", int(subjectID.Int32)),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
				slog.Int64("signal_internal", signalID),
			)
			return SkippedSubjectMissing, fmt.Sprintf("subject %s/%d not found", subjectType, subjectID.Int32), nil
		}
	}

	// Filter 4 — per-kind dedupe window. Only stateful kinds get the
	// gate; discrete kinds (manual, weather observation, calendar
	// event-day arrival) are always eligible because every row is a
	// distinct event in its own right.
	if def.Retention == signalkinds.RetentionStateful {
		duplicate, dErr := m.recentSameKindSubject(ctx, workspaceID, kind, subjectType, subjectID, signalID, now(), window)
		if dErr != nil {
			return SkippedDedupe, "dedupe lookup failed", fmt.Errorf("signaljudge.matcher: dedupe lookup: %w", dErr)
		}
		if duplicate {
			logger.DebugContext(ctx, "signaljudge.matcher: skip dedupe",
				slog.String("kind", string(kind)),
				slog.String("subject_type", subjectType),
				slog.Uint64("workspace_internal", uint64(workspaceID)),
				slog.Int64("signal_internal", signalID),
				slog.Duration("window", window),
			)
			return SkippedDedupe, fmt.Sprintf("stateful kind %q within %s window", kind, window), nil
		}
	}

	return Eligible, "", nil
}

// aiEnabled reads ai_settings.auto_action_enabled for the workspace.
// A missing row defaults to true (table default) so workspaces with
// no settings row are not silently locked out of the judge loop.
func (m *Matcher) aiEnabled(ctx context.Context, workspaceID uint32) (bool, error) {
	const q = `SELECT auto_action_enabled FROM ai_settings WHERE workspace_id = ? LIMIT 1`
	var enabled bool
	err := m.DB.QueryRowContext(ctx, q, workspaceID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// subjectExists checks whether the named subject row is present and
// enabled inside the workspace. The query is a small switch instead
// of dynamic SQL so the table list is auditable in one place; new
// subject types added to signalkinds.SubjectType must be added here
// in the same PR.
func (m *Matcher) subjectExists(ctx context.Context, workspaceID uint32, subjectType string, subjectID int32) (bool, error) {
	var query string
	switch generated.SignalsSubjectType(subjectType) {
	case generated.SignalsSubjectTypeTask:
		query = `SELECT 1 FROM tasks WHERE workspace_id = ? AND id = ? AND enabled = TRUE LIMIT 1`
	case generated.SignalsSubjectTypeUser:
		query = `SELECT 1 FROM workspace_members WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	case generated.SignalsSubjectTypeCalendarEvent:
		query = `SELECT 1 FROM calendar_events WHERE workspace_id = ? AND id = ? AND enabled = TRUE LIMIT 1`
	case generated.SignalsSubjectTypeWorkspace:
		// Workspace subjects are implicit in workspace_id, never have
		// a subject_id row to look up.
		return true, nil
	default:
		// Unknown subject type — treat as missing so we never enqueue
		// against a row we cannot verify.
		return false, nil
	}
	var one int
	err := m.DB.QueryRowContext(ctx, query, workspaceID, subjectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// recentSameKindSubject reports whether a signal with the same
// (workspace, kind, subject) was received inside the last window
// before the current signal. Excludes the current signal id so the
// row we are evaluating does not appear as its own duplicate.
//
// The query scans `signals` directly; a covering index on
// (workspace_id, subject_type, subject_id, received_at) lands with
// Phase 1 D1 so this stays cheap.
func (m *Matcher) recentSameKindSubject(ctx context.Context, workspaceID uint32, kind signalkinds.Kind, subjectType string, subjectID sql.NullInt32, currentSignalID int64, now time.Time, window time.Duration) (bool, error) {
	cutoff := now.Add(-window)
	if subjectID.Valid {
		const q = `SELECT 1 FROM signals
			WHERE workspace_id = ?
			  AND kind = ?
			  AND subject_type = ?
			  AND subject_id = ?
			  AND id <> ?
			  AND received_at >= ?
			LIMIT 1`
		var one int
		err := m.DB.QueryRowContext(ctx, q, workspaceID, string(kind), subjectType, subjectID.Int32, currentSignalID, cutoff).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	}
	const q = `SELECT 1 FROM signals
		WHERE workspace_id = ?
		  AND kind = ?
		  AND subject_type = ?
		  AND subject_id IS NULL
		  AND id <> ?
		  AND received_at >= ?
		LIMIT 1`
	var one int
	err := m.DB.QueryRowContext(ctx, q, workspaceID, string(kind), subjectType, currentSignalID, cutoff).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
