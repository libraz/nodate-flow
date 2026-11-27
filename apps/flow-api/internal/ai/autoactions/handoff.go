package autoactions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// HandoffQuerier is the narrow set of writes the auto-action executor
// needs to apply a handoff_to_user action. Kept as a small interface so
// unit tests can pass a fake without standing up a real DB. Production
// wiring is [txHandoff], which binds every call to the pass's
// transaction.
type HandoffQuerier interface {
	// RecordHandBack records the agent.task.handoff_to_user event
	// through the shared hand-back recorder.
	RecordHandBack(ctx context.Context, workspaceID uint32, h mutationlog.HandBack) error
	GetTaskAgentMemo(ctx context.Context, arg generated.GetTaskAgentMemoParams) (json.RawMessage, error)
	UpdateTaskAgentMemo(ctx context.Context, arg generated.UpdateTaskAgentMemoParams) (int64, error)
}

// ActorDisabler disables a single (workspace_id, task_id, agent_id)
// task_actors row by setting enabled = FALSE. The production
// implementation wraps the pass's transaction; tests pass a fake that records the
// disable for assertions.
type ActorDisabler interface {
	DisableAgentActor(ctx context.Context, workspaceID, taskID, agentID uint32) error
}

// defaultHandoffLoopLimit caps automated handoffs per task before the
// auto-action executor gives up and skips emitting further handoff
// events. Mirrors the runtime's NF_AGENT_HANDOFF_LOOP_LIMIT default
// so the same memo counter caps both code paths.
const defaultHandoffLoopLimit = 5

// txHandoff is the production [HandoffQuerier]: the memo statements
// and the hand-back event all run on the pass's transaction.
type txHandoff struct {
	*generated.Queries
	tx  *dbretry.Tx
	rec *mutationlog.Recorder
}

func (q txHandoff) RecordHandBack(ctx context.Context, workspaceID uint32, h mutationlog.HandBack) error {
	return q.rec.RecordHandBack(ctx, q.tx, mutationlog.Actor{WorkspaceID: workspaceID}, h)
}

// handoffToUser is the production wiring around [applyHandoffToUser].
// It runs the shared logic inside [dbretry.InTx], so the hand-back
// event fans out to the notify hooks once the transaction commits.
func (e *Executor) handoffToUser(ctx context.Context, r taskRow, act *Action) {
	if !r.hasAgentAssignee || r.agentID == 0 {
		// Defensive guard: the rule already filters for an agent
		// assignee, but we re-check here so a stale row read does
		// not turn into an event with no actor.
		return
	}
	rec := mutationlog.New(e.DB, generated.New(e.DB))
	var emitted bool
	err := dbretry.InTx(ctx, e.DB, "autoactions.handoffToUser", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		q := txHandoff{Queries: generated.New(tx), tx: tx, rec: rec}
		var err error
		emitted, err = e.applyHandoffToUser(ctx, q, &txActorDisabler{tx: tx}, r, act, time.Now().UTC())
		return err
	})
	if err != nil {
		e.Logger.Error("auto-action executor: handoff_to_user", "task", r.publicID.String(), "err", err)
		return
	}
	if emitted {
		e.Logger.Info("auto-action applied: handoff_to_user",
			"task", r.publicID.String(),
			"agent", r.agentPublicID.String(),
		)
	} else {
		e.Logger.Info("auto-action skipped: handoff_to_user (loop budget exhausted)",
			"task", r.publicID.String(),
			"agent", r.agentPublicID.String(),
		)
	}
}

// applyHandoffToUser is the pure logic of the handoff_to_user action.
// It is split out so unit tests can drive it with a fake HandoffQuerier
// + ActorDisabler. The bool return value indicates whether a handoff
// event was actually emitted (false when the loop budget is exhausted).
//
// Behaviour mirrors the orchestrator runner's handleHandoff:
//   - Read prior handoff_count from agent_memo.
//   - If prior >= HandoffLoopLimit (defaultHandoffLoopLimit when zero),
//     skip the emission and return (false, nil) so the caller can log.
//   - Otherwise emit agent.task.handoff_to_user with actor_agent_id set,
//     disable the agent task_actors row, and merge handoff state into
//     agent_memo (status=handed_back, reason=stuck, count=prior+1,
//     last_handoff_at=now unix seconds).
func (e *Executor) applyHandoffToUser(
	ctx context.Context,
	q HandoffQuerier,
	disabler ActorDisabler,
	r taskRow,
	act *Action,
	at time.Time,
) (bool, error) {
	limit := e.HandoffLoopLimit
	if limit <= 0 {
		limit = defaultHandoffLoopLimit
	}
	priorAttempts, priorCount := readMemoCounters(ctx, q, r.workspaceID, r.id)
	if priorCount >= limit {
		// Budget exhausted: do not emit another handoff. The
		// orchestrator runtime owns the loop-detected failure event;
		// the auto-action path simply refuses to add to the noise.
		return false, nil
	}

	payload := map[string]any{
		"reason":        "stuck",
		"agentPublicId": r.agentPublicID.String(),
		"taskPublicId":  r.publicID.String(),
		"handoffCount":  priorCount + 1,
		"attempts":      priorAttempts,
		"detectedBy":    "auto_action",
	}
	if act != nil && act.Reason != "" {
		payload["detail"] = act.Reason
	}
	if err := q.RecordHandBack(ctx, r.workspaceID, mutationlog.HandBack{
		TaskID:       r.id,
		TaskPublicID: r.publicID.String(),
		PriorAgentID: r.agentID,
		Payload:      payload,
		CallSite:     "autoactions.handoffToUser",
	}); err != nil {
		return false, fmt.Errorf("record handoff event: %w", err)
	}

	if err := disabler.DisableAgentActor(ctx, r.workspaceID, r.id, r.agentID); err != nil {
		return false, fmt.Errorf("disable agent actor: %w", err)
	}

	memoPatch := map[string]any{
		"handoff_status":  "handed_back",
		"handoff_reason":  "stuck",
		"handoff_count":   priorCount + 1,
		"last_handoff_at": at.Unix(),
	}
	patchRaw, err := json.Marshal(memoPatch)
	if err != nil {
		return false, fmt.Errorf("marshal memo patch: %w", err)
	}
	// The querier and the disabler run on one transaction, so this is the
	// last write of the three and the one that can still take the other two
	// back out.
	//
	// The statement matches an enabled task only, so a zero count is the
	// task having been disabled or removed since the scan chose it. The
	// event inserted above announces a hand-back and the assignee row is
	// already off the task, while the counter that caps the loop never
	// moved — so the next pass reads the same budget and hands the same
	// task back again. Failing here is what rolls all of it back, and it is
	// why the caller may not be told a handoff was applied.
	memoRows, err := q.UpdateTaskAgentMemo(ctx, generated.UpdateTaskAgentMemoParams{
		Column1:     patchRaw,
		WorkspaceID: r.workspaceID,
		ID:          r.id,
	})
	if err != nil {
		return false, fmt.Errorf("update agent_memo: %w", err)
	}
	if memoRows == 0 {
		return false, fmt.Errorf("agent_memo unchanged for task %d: absent or disabled", r.id)
	}
	return true, nil
}

// readMemoCounters reads attempts + handoff_count out of agent_memo so
// applyHandoffToUser can decide whether to emit and what counter to
// stamp on the new event / memo patch. Missing rows / decode errors
// yield zeros — the caller treats those as "first attempt".
func readMemoCounters(ctx context.Context, q HandoffQuerier, workspaceID, taskID uint32) (attempts, handoffCount int) {
	raw, err := q.GetTaskAgentMemo(ctx, generated.GetTaskAgentMemoParams{
		WorkspaceID: workspaceID,
		ID:          taskID,
	})
	if err != nil || len(raw) == 0 {
		return 0, 0
	}
	var memo struct {
		Attempts     int `json:"attempts"`
		HandoffCount int `json:"handoff_count"`
	}
	if err := json.Unmarshal(raw, &memo); err != nil {
		return 0, 0
	}
	return memo.Attempts, memo.HandoffCount
}

// txActorDisabler implements ActorDisabler over a transaction by issuing a
// targeted UPDATE against task_actors. We match on (workspace_id,
// task_id, agent_id, kind='agent', role='assignee') so a stale row in
// some other role for the same agent does not get disabled.
type txActorDisabler struct{ tx *dbretry.Tx }

// DisableAgentActor flips enabled=FALSE on the agent assignee row.
//
// A zero count is an error rather than a quiet success. The row was seen
// by the scan that selected this task, so zero means it went away in
// between — the agent was taken off the task by something else. The
// caller has already inserted the handoff event and is about to bump the
// handoff counter in agent_memo, and both of those describe a hand-back
// that this run did not perform; failing here is what rolls them back.
func (d *txActorDisabler) DisableAgentActor(ctx context.Context, workspaceID, taskID, agentID uint32) error {
	const q = `UPDATE task_actors
		SET enabled = FALSE
		WHERE workspace_id = ?
		  AND task_id = ?
		  AND agent_id = ?
		  AND kind = 'agent'
		  AND role = 'assignee'
		  AND enabled = TRUE`
	res, err := d.tx.ExecContext(ctx, q, workspaceID, taskID, agentID)
	if err != nil {
		return err
	}
	disabled, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if disabled == 0 {
		return fmt.Errorf("no enabled agent assignee row for task %d, agent %d", taskID, agentID)
	}
	return nil
}
