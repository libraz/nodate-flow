package agentruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
)

// ExecutionResult is the optional rich return value an [AgentExecutor]
// may produce alongside an error. The runner uses it to drive handoff
// triggers and tasks.agent_memo bookkeeping:
//
//   - Confidence is the LLM-reported confidence score for the latest
//     reply (0.0–1.0). Zero means "not reported" and skips the
//     low-confidence handoff trigger.
//   - LastThought is a short summary string written to agent_memo on
//     successful runs; the runner truncates it to 500 chars.
//   - ToolCalls counts tool invocations made during the run.
//   - ConsecutiveToolFailures counts trailing tool-call failures —
//     three or more triggers a tool_error handoff.
//   - CostMicros is the spend in millionths of a USD for this run; surfaces
//     in agent_memo.last_cost_micros. CostCents remains as a legacy display
//     field rounded down from CostMicros.
//   - CostCapHit is true when the cost guard rejected the call;
//     triggers the cost_cap handoff and pauses the agent.
type ExecutionResult struct {
	Confidence              float64
	LastThought             string
	ToolCalls               int
	ConsecutiveToolFailures int
	CostMicros              int64
	CostCents               int64
	CostCapHit              bool
}

// AgentExecutor is the narrow dependency that OrchestratorRunner needs
// from the ai package. Production wiring passes an adapter around
// [ai.Orchestrator] that loads the agent row, resolves the workspace
// provider, and calls Complete with the agent's system prompt. Tests
// pass a stub that records calls without touching a real LLM client.
//
// Splitting the interface here (instead of taking *ai.Orchestrator
// directly) keeps agentruntime free of the ai import cycle: the ai
// package already depends on agentruntime's sibling packages, so
// inverting the dependency at this boundary is what lets main.go wire
// everything together without a helper package.
type AgentExecutor interface {
	// ExecuteAgent runs one tick of the agent identified by agentID in
	// the given workspace. Implementations resolve the agent's model +
	// system prompt, invoke the provider, and return any error so the
	// runner can Nack the queue row and append an ai.agent.run.failed
	// event. The returned ExecutionResult is optional — implementations
	// that do not yet surface confidence / tool-call metadata return a
	// zero value and the runner treats it as "no signal".
	ExecuteAgent(ctx context.Context, workspaceID, agentID uint32) (ExecutionResult, error)
}

// JudgeExecutor is the narrow dependency the runner needs to dispatch
// a signal_judge agent (ADR 0008 D3). Production wiring passes an
// adapter around the signaljudge.Runner so this package stays free of
// an import cycle with the ai stack.
//
// The runner calls ExecuteJudge instead of ExecuteAgent when the
// claimed agent_runs row carries a judge-shaped dedupe_key. The
// signal id is parsed out of the dedupe_key by the runner; the
// JudgeExecutor only sees the (workspaceID, agentID, signalID)
// triple. A nil JudgeExecutor disables signal_judge dispatch and the
// runner falls back to the task-agent path with a warning — this is
// the safe default for deployments that have not opted into the
// judge wiring yet.
type JudgeExecutor interface {
	ExecuteJudge(ctx context.Context, workspaceID, agentID uint32, signalID int64) (ExecutionResult, error)
}

// handoffReason enumerates the structured reasons the orchestrator
// emits an agent → user handoff. Stored on the payload so the inbox
// UI can group / filter and the audit trail stays grep-able.
const (
	handoffReasonLowConfidence = "low_confidence"
	handoffReasonCostCap       = "cost_cap"
	handoffReasonToolError     = "tool_error"
)

// lowConfidenceThreshold is the LLM-reported confidence below which a
// successful run is treated as "stuck" and handed back to a human.
const lowConfidenceThreshold = 0.5

// toolErrorThreshold is the number of trailing tool-call failures in
// one run that escalates the agent into a handoff. Three is the
// product-decided ceiling — fewer is noise, more is unrecoverable.
const toolErrorThreshold = 3

// defaultHandoffLoopLimit caps automated handoffs per task before the
// orchestrator gives up and pauses the agent. The runtime reads the
// effective limit from OrchestratorRunner.HandoffLoopLimit (wired from
// NF_AGENT_HANDOFF_LOOP_LIMIT) and falls back to this constant when
// the field is zero.
const defaultHandoffLoopLimit = 5

// agentMemoSnapshot is the narrow read-projection of tasks.agent_memo
// the runner needs to make handoff / loop-budget decisions. New keys
// are added to the patch via UpdateTaskAgentMemo without touching this
// struct; only fields the runner has to *read* live here.
type agentMemoSnapshot struct {
	Attempts      int    `json:"attempts"`
	HandoffCount  int    `json:"handoff_count"`
	HandoffStatus string `json:"handoff_status"`
}

// RunnerQuerier is the narrow slice of [generated.Querier] the
// orchestrator runner depends on. Kept as a small interface so tests
// can pass a hand-rolled fake without re-implementing the entire sqlc
// bundle. The production wiring satisfies it with a *generated.Queries
// pointer constructed via generated.New(db).
type RunnerQuerier interface {
	AppendAgentEvent(ctx context.Context, arg generated.AppendAgentEventParams) (int64, error)
	InsertHandoffToUserEvent(ctx context.Context, arg generated.InsertHandoffToUserEventParams) (int64, error)
	GetTaskAgentMemo(ctx context.Context, arg generated.GetTaskAgentMemoParams) (json.RawMessage, error)
	UpdateTaskAgentMemo(ctx context.Context, arg generated.UpdateTaskAgentMemoParams) error
}

// OrchestratorRunner is a [Runner] that delegates the LLM call to an
// [AgentExecutor] and writes ai.agent.run.* events around it. It
// replaces [LogRunner] once the api is wired to a real orchestrator;
// the LogRunner stays as the single-binary scaffold default so tests
// and smoke runs do not need a provider configured.
type OrchestratorRunner struct {
	DB       *sql.DB
	Executor AgentExecutor
	// Judge is the dispatch hook for signal_judge agents (ADR 0008
	// D3). When non-nil, claimed rows whose dedupe_key carries the
	// `judge:<agent>:<signal>` shape route through ExecuteJudge
	// instead of ExecuteAgent. A nil Judge falls back to the
	// AgentExecutor with a warning, so deployments that have not
	// opted into the judge wiring still degrade safely.
	Judge JudgeExecutor
	// Queries is the sqlc bundle used to update tasks.agent_memo,
	// insert handoff events, and emit run events with actor_agent_id.
	// May be nil during early single-binary smoke tests; the runner
	// degrades to "best-effort" event-only behavior in that case.
	Queries RunnerQuerier
	// HandoffLoopLimit caps automated handoffs per task before the
	// runner pauses the agent and emits HANDOFF_LOOP_DETECTED.
	// Zero falls back to defaultHandoffLoopLimit.
	HandoffLoopLimit int
	// Now is injected so tests can assert deterministic event
	// timestamps; defaults to time.Now when nil.
	Now func() time.Time
}

// Run implements [Runner]. It appends ai.agent.run.started before the
// call and ai.agent.run.completed / ai.agent.run.failed after,
// stamped with actor_agent_id=agent.id and task_id sourced from the
// triggering event. On completion it inspects the ExecutionResult to
// fire automated handoff events when the agent looks stuck.
//
// Event emissions are best-effort: a failing append is logged and
// swallowed so a flaky events table cannot wedge the agent loop.
func (r *OrchestratorRunner) Run(ctx context.Context, j Job, _ time.Time) error {
	if r.Now == nil {
		r.Now = time.Now
	}
	// Tag the context so any nested emitters (MCP tool calls, future
	// orchestrator sub-spans) attribute their event rows to the agent
	// actor rather than the human session that may have started the
	// request chain.
	ctx = eventbus.WithActorAgentID(ctx, j.AgentID)

	agentPubID := r.resolveAgentPublicID(ctx, j.AgentID)
	taskInternalID, taskPubID := r.resolveSourceTask(ctx, j)

	started := r.Now().UTC()
	r.bumpAttempts(ctx, j.WsID, taskInternalID, started)
	r.appendRunEvent(ctx, runEventArgs{
		eventType:   eventbus.AiAgentRunStarted,
		workspaceID: j.WsID,
		agentID:     j.AgentID,
		agentPubID:  agentPubID,
		taskID:      taskInternalID,
		occurred:    started,
		payload: map[string]any{
			"agentId":   agentPubID,
			"startedAt": started.Unix(),
		},
	})

	var (
		result ExecutionResult
		runErr error
	)
	// Dispatch path: a judge-shaped dedupe_key routes through the
	// JudgeExecutor when one is configured. Falling back to the
	// task-agent path is intentional — deployments that have not yet
	// wired the judge still execute the queue (so the row does not
	// stall), with a logged warning so the misconfiguration is
	// visible.
	if signalID, isJudge := parseJudgeDedupeKey(j.DedupeKey); isJudge {
		if r.Judge != nil {
			result, runErr = r.Judge.ExecuteJudge(ctx, j.WsID, j.AgentID, signalID)
		} else {
			slog.WarnContext(ctx, "agentruntime: judge dispatch with no JudgeExecutor configured; falling back to task-agent path",
				slog.Uint64("workspace_internal", uint64(j.WsID)),
				slog.Uint64("agent_internal", uint64(j.AgentID)),
				slog.Int64("signal_internal", signalID),
			)
			if r.Executor != nil {
				result, runErr = r.Executor.ExecuteAgent(ctx, j.WsID, j.AgentID)
			}
		}
	} else if r.Executor != nil {
		result, runErr = r.Executor.ExecuteAgent(ctx, j.WsID, j.AgentID)
	}
	finished := r.Now().UTC()

	if runErr != nil {
		r.recordFailureMemo(ctx, j.WsID, taskInternalID, finished, runErr.Error())
		r.appendRunEvent(ctx, runEventArgs{
			eventType:   eventbus.AiAgentRunFailed,
			workspaceID: j.WsID,
			agentID:     j.AgentID,
			agentPubID:  agentPubID,
			taskID:      taskInternalID,
			occurred:    finished,
			payload: map[string]any{
				"agentId":    agentPubID,
				"startedAt":  started.Unix(),
				"finishedAt": finished.Unix(),
				"error":      runErr.Error(),
			},
		})
		// Cost-cap is the one structured failure the runner also has
		// to react to: pause the agent and emit a handoff so the task
		// reaches a human before more spend lands.
		if result.CostCapHit {
			r.handleHandoff(ctx, handoffReasonCostCap, j, taskInternalID, taskPubID, agentPubID, finished, runErr.Error())
		}
		return runErr
	}

	// Successful run — decide whether to record success or fire a
	// handoff. The triggers are checked in priority order: confidence
	// first (cheap), then tool-error (rare), with cost-cap already
	// handled in the failure path above.
	if reason := classifyHandoff(result); reason != "" {
		r.handleHandoff(ctx, reason, j, taskInternalID, taskPubID, agentPubID, finished, "")
	} else {
		r.recordSuccessMemo(ctx, j.WsID, taskInternalID, finished, result)
	}

	r.appendRunEvent(ctx, runEventArgs{
		eventType:   eventbus.AiAgentRunCompleted,
		workspaceID: j.WsID,
		agentID:     j.AgentID,
		agentPubID:  agentPubID,
		taskID:      taskInternalID,
		occurred:    finished,
		payload: map[string]any{
			"agentId":    agentPubID,
			"startedAt":  started.Unix(),
			"finishedAt": finished.Unix(),
		},
	})
	return nil
}

// classifyHandoff inspects a successful run's result and returns the
// matching handoff reason, or "" when the run looks healthy.
// Cost-cap is intentionally absent: it is signalled via runErr so
// callers handle it on the failure branch.
func classifyHandoff(result ExecutionResult) string {
	if result.Confidence > 0 && result.Confidence < lowConfidenceThreshold {
		return handoffReasonLowConfidence
	}
	if result.ConsecutiveToolFailures >= toolErrorThreshold {
		return handoffReasonToolError
	}
	return ""
}

// runEventArgs bundles the inputs to appendRunEvent so call sites
// stay readable when adding/removing payload keys.
type runEventArgs struct {
	eventType   string
	workspaceID uint32
	agentID     uint32
	agentPubID  string
	taskID      uint32
	occurred    time.Time
	payload     map[string]any
}

func (r *OrchestratorRunner) appendRunEvent(ctx context.Context, args runEventArgs) {
	q := r.queries()
	if q == nil {
		return
	}
	raw, err := json.Marshal(args.payload)
	if err != nil {
		slog.WarnContext(ctx, "agentruntime: marshal run event payload", "err", err, "type", args.eventType)
		return
	}
	taskID := sql.NullInt32{}
	if args.taskID != 0 {
		taskID = sql.NullInt32{Int32: int32(args.taskID), Valid: true} //#nosec G115 -- task_id sourced from events.task_id, fits int32 within realistic deployments
	}
	actorAgentID := sql.NullInt32{}
	if args.agentID != 0 {
		actorAgentID = sql.NullInt32{Int32: int32(args.agentID), Valid: true} //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
	}
	if _, err := q.AppendAgentEvent(ctx, generated.AppendAgentEventParams{
		PublicID:     types.New(),
		WorkspaceID:  args.workspaceID,
		TaskID:       taskID,
		ActorAgentID: actorAgentID,
		Type:         args.eventType,
		PayloadJson:  raw,
		OccurredAt:   args.occurred,
	}); err != nil {
		slog.WarnContext(ctx, "agentruntime: append agent run event failed",
			"err", err, "type", args.eventType, "workspace_id", args.workspaceID, "agent_id", args.agentID)
	}
}

// queries returns the runner's RunnerQuerier, falling back to a fresh
// generated.New(r.DB) wrapper when no explicit Queries is configured.
// This keeps single-binary smoke deployments compiling without an
// explicit wiring step while still letting tests inject a fake.
func (r *OrchestratorRunner) queries() RunnerQuerier {
	if r.Queries != nil {
		return r.Queries
	}
	if r.DB == nil {
		return nil
	}
	return generated.New(r.DB)
}

// resolveSourceTask looks up the source event's task_id when the run
// was triggered by an on_event fan-out. Returns (0, "") for interval
// or manual runs that carry no source event id.
func (r *OrchestratorRunner) resolveSourceTask(ctx context.Context, j Job) (uint32, string) {
	if r.DB == nil || j.SourceEventID == 0 {
		return 0, ""
	}
	const q = `SELECT e.task_id, t.public_id FROM events e
		LEFT JOIN tasks t ON t.id = e.task_id
		WHERE e.id = ? AND e.workspace_id = ? LIMIT 1`
	var (
		taskID sql.NullInt32
		pubRaw []byte
		pubID  types.PublicID
	)
	row := r.DB.QueryRowContext(ctx, q, j.SourceEventID, j.WsID)
	if err := row.Scan(&taskID, &pubRaw); err != nil {
		slog.WarnContext(ctx, "agentruntime: source-task lookup failed",
			slog.Uint64("event_id", uint64(j.SourceEventID)), slog.Any("err", err))
		return 0, ""
	}
	if !taskID.Valid {
		return 0, ""
	}
	if len(pubRaw) == 16 {
		_ = pubID.Scan(pubRaw)
	}
	//#nosec G115 -- tasks.id is INT UNSIGNED, fits uint32
	return uint32(taskID.Int32), pubID.String()
}

// resolveAgentPublicID fetches the public_id (UUID v7) for the given
// internal agent ID. Returns "unknown" if the lookup fails, so event
// payloads never contain the raw uint32.
func (r *OrchestratorRunner) resolveAgentPublicID(ctx context.Context, agentID uint32) string {
	if r.DB == nil {
		return "unknown"
	}
	const q = `SELECT public_id FROM ai_agents WHERE id = ? LIMIT 1`
	var pubID types.PublicID
	if err := r.DB.QueryRowContext(ctx, q, agentID).Scan(&pubID); err != nil {
		slog.WarnContext(ctx, "agentruntime: failed to resolve agent public_id",
			slog.Uint64("agent_id", uint64(agentID)), slog.Any("err", err))
		return "unknown"
	}
	return pubID.String()
}

// readMemo reads the task's agent_memo blob and decodes the narrow
// projection the runner uses for decisions. Missing rows / NULL memo
// values are treated as zero — every memo write is JSON_MERGE_PATCH,
// so partial state is always valid.
func (r *OrchestratorRunner) readMemo(ctx context.Context, workspaceID, taskID uint32) agentMemoSnapshot {
	var snap agentMemoSnapshot
	q := r.queries()
	if q == nil || taskID == 0 {
		return snap
	}
	raw, err := q.GetTaskAgentMemo(ctx, generated.GetTaskAgentMemoParams{
		WorkspaceID: workspaceID,
		ID:          taskID,
	})
	if err != nil || len(raw) == 0 {
		return snap
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		slog.WarnContext(ctx, "agentruntime: agent_memo decode failed",
			slog.Uint64("task_id", uint64(taskID)), slog.Any("err", err))
	}
	return snap
}

// mergeMemo applies a JSON_MERGE_PATCH update against tasks.agent_memo.
// A nil Queries (single-binary smoke path) or zero task_id is a no-op.
func (r *OrchestratorRunner) mergeMemo(ctx context.Context, workspaceID, taskID uint32, patch map[string]any) {
	q := r.queries()
	if q == nil || taskID == 0 || len(patch) == 0 {
		return
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		slog.WarnContext(ctx, "agentruntime: agent_memo marshal failed", "err", err)
		return
	}
	if err := q.UpdateTaskAgentMemo(ctx, generated.UpdateTaskAgentMemoParams{
		Column1:     raw,
		WorkspaceID: workspaceID,
		ID:          taskID,
	}); err != nil {
		slog.WarnContext(ctx, "agentruntime: agent_memo update failed",
			slog.Uint64("task_id", uint64(taskID)), slog.Any("err", err))
	}
}

// bumpAttempts is the run-start memo write — increments attempts and
// stamps last_started_at. It reads the prior attempts count so the
// JSON_MERGE_PATCH semantics produce a monotonic counter even though
// the column type is just a JSON number.
func (r *OrchestratorRunner) bumpAttempts(ctx context.Context, workspaceID, taskID uint32, at time.Time) {
	if taskID == 0 {
		return
	}
	prior := r.readMemo(ctx, workspaceID, taskID)
	r.mergeMemo(ctx, workspaceID, taskID, map[string]any{
		"attempts":        prior.Attempts + 1,
		"last_started_at": at.Unix(),
	})
}

// recordSuccessMemo writes the run-complete fields and clears any
// prior "stuck" handoff_status so the inbox UI returns the task to its
// normal flow once the agent recovers.
func (r *OrchestratorRunner) recordSuccessMemo(ctx context.Context, workspaceID, taskID uint32, at time.Time, result ExecutionResult) {
	if taskID == 0 {
		return
	}
	costMicros := result.CostMicros
	if costMicros == 0 && result.CostCents > 0 {
		costMicros = result.CostCents * 10_000
	}
	patch := map[string]any{
		"last_finished_at": at.Unix(),
		"last_tool_calls":  result.ToolCalls,
		"last_cost_micros": costMicros,
		"last_cost_cents":  costMicros / 10_000,
	}
	if result.LastThought != "" {
		patch["last_thought"] = truncate(result.LastThought, 500)
	}
	prior := r.readMemo(ctx, workspaceID, taskID)
	if prior.HandoffStatus != "" {
		// JSON null clears the key via RFC 7396 merge semantics.
		patch["handoff_status"] = nil
		patch["handoff_reason"] = nil
	}
	r.mergeMemo(ctx, workspaceID, taskID, patch)
}

// recordFailureMemo writes the last_error / last_finished_at fields.
// handoff_status is set by handleHandoff when the failure triggers a
// handoff; pure failures leave the prior status untouched.
func (r *OrchestratorRunner) recordFailureMemo(ctx context.Context, workspaceID, taskID uint32, at time.Time, msg string) {
	if taskID == 0 {
		return
	}
	r.mergeMemo(ctx, workspaceID, taskID, map[string]any{
		"last_finished_at": at.Unix(),
		"last_error":       msg,
	})
}

// handleHandoff is the unified path for emitting an agent → user
// handoff. It enforces the per-task loop budget: when the prior
// handoff_count is at or above the limit, no further handoff events
// are emitted and the agent is paused with a HANDOFF_LOOP_DETECTED
// ai.agent.run.failed event instead.
func (r *OrchestratorRunner) handleHandoff(ctx context.Context, reason string, j Job, taskID uint32, taskPubID, agentPubID string, at time.Time, lastError string) {
	limit := r.HandoffLoopLimit
	if limit <= 0 {
		limit = defaultHandoffLoopLimit
	}
	prior := r.readMemo(ctx, j.WsID, taskID)
	if taskID != 0 && prior.HandoffCount >= limit {
		// Budget exhausted: pause the agent and emit a structured
		// failure event in place of the handoff so the inbox UI can
		// surface the loop-detection state.
		r.pauseAgent(ctx, j.AgentID)
		r.appendRunEvent(ctx, runEventArgs{
			eventType:   eventbus.AiAgentRunFailed,
			workspaceID: j.WsID,
			agentID:     j.AgentID,
			agentPubID:  agentPubID,
			taskID:      taskID,
			occurred:    at,
			payload: map[string]any{
				"agentId":      agentPubID,
				"finishedAt":   at.Unix(),
				"error":        apierrors.WsTaskAgentHandoffLoopDetected.Code,
				"reason":       reason,
				"handoffCount": prior.HandoffCount,
			},
		})
		r.mergeMemo(ctx, j.WsID, taskID, map[string]any{
			"handoff_status":   "loop_detected",
			"handoff_reason":   reason,
			"last_finished_at": at.Unix(),
		})
		return
	}

	// Cost-cap also pauses the agent (existing semantics) — but it
	// pauses *and* emits a handoff so the human knows why.
	if reason == handoffReasonCostCap {
		r.pauseAgent(ctx, j.AgentID)
	}

	payload := map[string]any{
		"reason":        reason,
		"agentPublicId": agentPubID,
		"handoffCount":  prior.HandoffCount + 1,
	}
	if taskPubID != "" {
		payload["taskPublicId"] = taskPubID
	}
	if lastError != "" {
		payload["error"] = lastError
	}
	r.appendHandoffEvent(ctx, j, taskID, payload, at)

	memoPatch := map[string]any{
		"handoff_status":   "stuck",
		"handoff_reason":   reason,
		"handoff_count":    prior.HandoffCount + 1,
		"last_handoff_at":  at.Unix(),
		"last_finished_at": at.Unix(),
	}
	if lastError != "" {
		memoPatch["last_error"] = lastError
	}
	r.mergeMemo(ctx, j.WsID, taskID, memoPatch)
}

// appendHandoffEvent writes the agent.task.handoff_to_user row. It
// uses the dedicated InsertHandoffToUserEvent query (binds
// actor_agent_id, leaves actor_user_id NULL) rather than AppendAgentEvent
// because the handoff family has its own audit shape downstream.
func (r *OrchestratorRunner) appendHandoffEvent(ctx context.Context, j Job, taskID uint32, payload map[string]any, at time.Time) {
	q := r.queries()
	if q == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.WarnContext(ctx, "agentruntime: marshal handoff payload", "err", err)
		return
	}
	taskIDNull := sql.NullInt32{}
	if taskID != 0 {
		taskIDNull = sql.NullInt32{Int32: int32(taskID), Valid: true} //#nosec G115 -- task_id fits int32
	}
	actorAgent := sql.NullInt32{Int32: int32(j.AgentID), Valid: true} //#nosec G115 -- ai_agents.id fits int32
	if _, err := q.InsertHandoffToUserEvent(ctx, generated.InsertHandoffToUserEventParams{
		PublicID:     types.New(),
		WorkspaceID:  j.WsID,
		TaskID:       taskIDNull,
		ActorAgentID: actorAgent,
		PayloadJson:  raw,
		OccurredAt:   at,
	}); err != nil {
		slog.WarnContext(ctx, "agentruntime: handoff_to_user insert failed",
			slog.Uint64("agent_id", uint64(j.AgentID)), slog.Any("err", err))
	}
}

// pauseAgent flips ai_agents.paused=TRUE so subsequent ticks short-
// circuit. The runner reaches into the table directly because there is
// no narrow sqlc verb for "pause one agent" — the existing
// SetAgentPaused wrappers all require additional bookkeeping fields.
func (r *OrchestratorRunner) pauseAgent(ctx context.Context, agentID uint32) {
	if r.DB == nil {
		return
	}
	const q = `UPDATE ai_agents SET paused = TRUE WHERE id = ?`
	if _, err := r.DB.ExecContext(ctx, q, agentID); err != nil {
		slog.WarnContext(ctx, "agentruntime: pause agent failed",
			slog.Uint64("agent_id", uint64(agentID)), slog.Any("err", err))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
