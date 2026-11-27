package autoactions

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

// errTransitionRejected rolls an auto-action transition back when the
// state machine refuses the move — the task drifted between the query
// and the apply. Internal only, and not transient, so the retry loop
// leaves it alone.
var errTransitionRejected = stderrors.New("autoactions: transition rejected")

// escalate bumps priority by 1 (if not already max) and records an event.
// When the task is already at the priority cap there is nothing new to
// propose, so we return silently to avoid flooding the activity log
// with identical `ai.auto_action.proposed` events on every tick.
func (e *Executor) escalate(ctx context.Context, r taskRow, act *Action) {
	newPriority := r.priority + 1
	if newPriority > 4 {
		newPriority = 4
	}
	if newPriority == r.priority {
		// Already at cap (priority = 4). Nothing to escalate; skip the
		// proposal event entirely.
		return
	}

	// dbretry.InTx owns the transaction so the event's fan-out (realtime,
	// notifications, webhooks) fires after the commit. A hand-rolled
	// transaction has no boundary to defer that to, and the appenders do
	// not accept one.
	if err := dbretry.InTx(ctx, e.DB, "autoactions.escalate", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"UPDATE tasks SET priority = ?, updated_at = NOW() WHERE id = ?",
			newPriority, r.id,
		); err != nil {
			return err
		}
		taskInternal := int64(r.id)
		return eventbus.Append(ctx, tx, eventbus.Event{
			Type:        eventbus.TaskUpdated,
			WorkspaceID: r.workspaceID,
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"auto_action": string(act.Kind),
				"field":       "priority",
				"from":        r.priority,
				"to":          newPriority,
				"reason":      act.Reason,
			},
		})
	}); err != nil {
		e.Logger.Error("auto-action executor: escalate", "task", r.publicID.String(), "err", err)
		return
	}
	e.Logger.Info("auto-action applied: escalate",
		"task", r.publicID.String(),
		"priority", fmt.Sprintf("%d→%d", r.priority, newPriority),
	)
}

// closeStaleReview transitions a stale manual-policy review task to done
// through the canonical state-machine helper, which owns the
// derived_state write and the task.transition.complete event. A
// review_required task is skipped: only an approved submission completes
// it, and the review gate would refuse the attempt on every pass.
func (e *Executor) closeStaleReview(ctx context.Context, r taskRow, act *Action) {
	if r.completionPolicy == string(generated.TasksCompletionPolicyReviewRequired) {
		e.Logger.Debug("auto-action executor: stale review left to its reviewer",
			"task", r.publicID.String(),
			"kind", string(act.Kind),
		)
		return
	}
	e.applyStateTransition(ctx, r, act, taskstate.TransitionComplete)
}

// recordProposal writes an event recording the AI proposal without
// mutating the task. It deduplicates against the most recent
// `ai.auto_action.proposed` event for this task: if the last proposal
// had the same kind and no user-driven (non-`ai.%`) event has touched
// the task since, the new proposal is skipped. This prevents the
// activity log from filling up with identical rows every tick for
// tasks stuck in a state the rule keeps matching.
func (e *Executor) recordProposal(ctx context.Context, r taskRow, act *Action) {
	var lastKind sql.NullString
	var lastOccurred sql.NullTime
	err := e.DB.QueryRowContext(ctx, `
		SELECT JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.kind')), occurred_at
		FROM events
		WHERE task_id = ? AND type = ?
		ORDER BY occurred_at DESC
		LIMIT 1`, r.id, string(eventbus.AiAutoActionProposed)).Scan(&lastKind, &lastOccurred)
	if err == nil && lastKind.Valid && lastKind.String == string(act.Kind) && lastOccurred.Valid {
		var userSince int
		_ = e.DB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM events
			WHERE task_id = ? AND occurred_at > ? AND type NOT LIKE 'ai.%'`,
			r.id, lastOccurred.Time).Scan(&userSince)
		if userSince == 0 {
			// Same proposal kind is already the latest, and no user
			// activity since. Nothing new to say.
			return
		}
	}

	// The proposal is the whole write: there is no mutation for it to be
	// atomic with, and nothing downstream of this call depends on it. A
	// failure here costs the activity feed one line, which is not worth
	// abandoning the tick over, so it is recorded rather than propagated.
	taskInternal := int64(r.id)
	eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(e.DB), eventbus.Event{
		Type:        eventbus.AiAutoActionProposed,
		WorkspaceID: r.workspaceID,
		TaskID:      &taskInternal,
		Payload: map[string]any{
			"kind":       string(act.Kind),
			"confidence": act.Confidence,
			"reason":     act.Reason,
		},
	}, "autoactions.recordProposal")
}

// autoArchive sets archived_at on a completed/cancelled task that has
// been idle longer than the configured threshold.
//
// The write is a claim rather than a patch, so it can succeed and change
// nothing: the scan selects tasks whose archived_at is still NULL, and
// the predicate re-asserts that at write time, so a task archived by
// anyone else in between takes this pass's UPDATE down to zero rows. The
// event and the log below both describe an archive, and neither is true
// of a pass that archived nothing — an appended event would put an
// archive entry on the task's timeline with no archive behind it, one per
// pass for as long as the rule keeps matching the row.
func (e *Executor) autoArchive(ctx context.Context, r taskRow, act *Action) {
	// As in escalate: the transaction is opened through dbretry.InTx so
	// the archive event reaches its subscribers once the row is durable.
	//
	// archived is reset inside the closure because InTx re-runs it on a
	// transient error, and the attempt that commits is the one whose count
	// the log below is about.
	var archived int64
	if err := dbretry.InTx(ctx, e.DB, "autoactions.autoArchive", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		archived = 0
		res, err := tx.ExecContext(ctx,
			"UPDATE tasks SET archived_at = NOW(), updated_at = NOW() WHERE id = ? AND archived_at IS NULL",
			r.id,
		)
		if err != nil {
			return err
		}
		archived, err = res.RowsAffected()
		if err != nil {
			return err
		}
		// Zero is not a failure. The predicate guards on archived_at, so
		// the count answers whether this pass archived the task, never
		// whether the task is there — and a task that is already archived
		// is in the state the action wanted. There is nothing to undo and
		// nothing to report, so the transaction commits without the event.
		if archived == 0 {
			return nil
		}
		taskInternal := int64(r.id)
		return eventbus.Append(ctx, tx, eventbus.Event{
			Type:        eventbus.TaskArchived,
			WorkspaceID: r.workspaceID,
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"auto_action": string(act.Kind),
				"reason":      act.Reason,
			},
		})
	}); err != nil {
		e.Logger.Error("auto-action executor: auto-archive", "task", r.publicID.String(), "err", err)
		return
	}
	if archived == 0 {
		// Reported the way handoff_to_user reports an action with nothing
		// left to do: the pass ran and changed nothing, which is worth
		// seeing and is not an archive.
		e.Logger.Info("auto-action skipped: auto-archive (task already archived)",
			"task", r.publicID.String(),
			"state", r.derivedState,
		)
		return
	}
	e.Logger.Info("auto-action applied: auto-archive",
		"task", r.publicID.String(),
		"state", r.derivedState,
	)
}

// autoClose cancels a stale open task that has had no activity beyond
// the configured threshold. Like closeStaleReview, the actual
// derived_state UPDATE and matching task.transition.cancel event are
// performed by the canonical taskstate helper.
func (e *Executor) autoClose(ctx context.Context, r taskRow, act *Action) {
	e.applyStateTransition(ctx, r, act, taskstate.TransitionCancel)
}

// applyStateTransition is the shared body of [closeStaleReview] and
// [autoClose]. It opens a transaction, runs the canonical
// taskstate.ApplyTransitionTx helper (which acquires a row lock,
// validates the transition against the v1 state machine, persists the
// new derived_state, and appends the matching task.transition.<name>
// event) and commits.
//
// The executor runs as a system component, not on behalf of a user,
// so ActorUserID is left nil; the via="auto_action" tag and
// auto_action / confidence keys on the event payload mark the event
// origin so audit consumers can distinguish it from human transitions.
func (e *Executor) applyStateTransition(ctx context.Context, r taskRow, act *Action, transition string) {
	// dbretry.InTx owns the transaction so the transition event's
	// fan-out (realtime, notifications, webhooks) fires after the
	// commit. Applying the same transition by hand would append the
	// event but never deliver anything derived from it.
	var (
		result   taskstate.ApplyResult
		spec     *apierrors.Spec
		rejected error
	)
	txErr := dbretry.InTx(ctx, e.DB, "autoactions.applyStateTransition", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var cause error
		result, spec, cause = taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID: r.workspaceID,
			TaskID:      r.id,
			PublicID:    r.publicID,
			Transition:  transition,
			ActorUserID: nil, // system / auto-action, not a real user
			Reason:      act.Reason,
			Via:         "auto_action",
			ExtraPayload: map[string]any{
				"auto_action": string(act.Kind),
				"confidence":  act.Confidence,
			},
		})
		if spec != nil {
			// Validation rejection (TRANSITION_REJECTED / NOT_FOUND) means
			// the task drifted out of the matching state between query and
			// apply; this is benign — roll back, log and skip.
			e.Logger.Warn("auto-action executor: transition rejected",
				"task", r.publicID.String(),
				"transition", transition,
				"code", spec.Code,
				"cause", cause,
			)
			rejected = errTransitionRejected
			return rejected
		}
		return cause
	})
	if rejected != nil {
		return
	}
	if txErr != nil {
		e.Logger.Error("auto-action executor: transition failed", "task", r.publicID.String(), "err", txErr)
		return
	}
	e.Logger.Info("auto-action applied: state transition",
		"task", r.publicID.String(),
		"kind", string(act.Kind),
		"from", string(result.FromState),
		"to", string(result.ToState),
	)
}
