package ai

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// taskWorkPurpose is the ai_invocations purpose of a task-work step.
const taskWorkPurpose = "agent_task_step"

// taskWorkMemoBytes bounds the finish summary written to agent_memo.
const taskWorkMemoBytes = 500

// ErrTaskWorkUnavailable is returned for a task-work run on an executor
// that was built without a task service or its queries.
var ErrTaskWorkUnavailable = errors.New("ai: task work needs a task service and its queries")

var errTaskWorkNoRunKey = errors.New("ai: task-work run has no run key")

// TaskWorkService is the slice of *taskservice.Service a task-work run
// reads and writes through.
type TaskWorkService interface {
	ReadForAgent(ctx context.Context, agent execution.Principal, task types.PublicID) (taskservice.AgentTaskContext, error)
	Execute(ctx context.Context, run taskservice.RunPrincipal, task types.PublicID, a taskservice.Action, step taskservice.StepRef) (taskservice.Outcome, error)
	RecordStep(ctx context.Context, run taskservice.RunPrincipal, task types.PublicID, step taskservice.StepRef, rec taskservice.StepRecord) error
}

// TaskWorkQueries is the slice of the sqlc Querier the start-of-run checks
// need. *generated.Queries satisfies it.
type TaskWorkQueries interface {
	GetAssigneeDelegator(ctx context.Context, arg generated.GetAssigneeDelegatorParams) (sql.NullInt32, error)
	GetLatestTaskWorkContextEventID(ctx context.Context, arg generated.GetLatestTaskWorkContextEventIDParams) (int64, error)
	CountTaskWorkRunsSince(ctx context.Context, arg generated.CountTaskWorkRunsSinceParams) (int64, error)
}

// taskWorkRun is the state of one task-work run: one LLM call per move,
// each move handed to taskservice and recorded as a step, until the model
// finishes or a limit ends the run.
type taskWorkRun struct {
	e       *AgentExecutor
	ws      uint32
	agentID uint32
	trigger agentruntime.Trigger
	task    types.PublicID
	runKey  string

	agent     generated.GetAgentForExecRow
	delegator uint32
	run       taskservice.RunPrincipal
	prov      providers.Provider

	calls    int
	writes   int
	subtasks int
	failures int
	last     *taskWorkMove
	steps    []taskWorkStepNote
	result   agentruntime.ExecutionResult
}

// executeTaskWork runs task work for the trigger's task. Endings the runner
// reports as completed return a nil error; budget, timeout and error return
// the cause so the runner takes its failure path.
func (e *AgentExecutor) executeTaskWork(ctx context.Context, workspaceID, agentID uint32, trigger agentruntime.Trigger) (agentruntime.ExecutionResult, error) {
	r := &taskWorkRun{e: e, ws: workspaceID, agentID: agentID, trigger: trigger, runKey: agentruntime.RunKeyFromContext(ctx)}
	if e.TaskService == nil || e.TaskWork == nil {
		return r.fail(endedError, ErrTaskWorkUnavailable)
	}
	if r.runKey == "" {
		return r.fail(endedError, errTaskWorkNoRunKey)
	}
	task, err := types.Parse(trigger.TaskPublicID)
	if err != nil {
		return r.fail(endedError, fmt.Errorf("ai: task-work trigger task: %w", err))
	}
	r.task = task
	timeout := e.taskWorkTimeout
	if timeout <= 0 {
		timeout = TaskWorkTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return r.start(ctx)
}

// start runs the checks that end a run before any LLM call, then the moves.
func (r *taskWorkRun) start(ctx context.Context) (agentruntime.ExecutionResult, error) {
	row, err := r.e.Queries.GetAgentForExec(ctx, generated.GetAgentForExecParams{WorkspaceID: r.ws, ID: r.agentID})
	if errors.Is(err, sql.ErrNoRows) {
		return r.end(endedStopped)
	}
	if err != nil {
		return r.failCtx(ctx, fmt.Errorf("ai: agent lookup failed: %w", err))
	}
	if row.Paused {
		return r.end(endedStopped)
	}
	r.agent = row

	delegator, err := r.e.TaskWork.GetAssigneeDelegator(ctx, generated.GetAssigneeDelegatorParams{
		WorkspaceID: r.ws, TaskID: r.trigger.TaskID, AgentID: nullID(r.agentID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return r.end(endedStopped)
	}
	if err != nil {
		return r.failCtx(ctx, err)
	}
	if delegator.Valid {
		r.delegator = uint32(delegator.Int32) //#nosec G115 -- users.id is INT UNSIGNED, read back non-negative
	}
	tc, stop, err := r.read(ctx)
	if err != nil {
		return r.failCtx(ctx, err)
	}
	if stop {
		return r.end(endedStopped)
	}

	covered, err := r.e.TaskWork.GetLatestTaskWorkContextEventID(ctx, generated.GetLatestTaskWorkContextEventIDParams{
		WorkspaceID: r.ws, TaskID: nullID(r.trigger.TaskID), AgentID: nullID(r.agentID),
		CompletedType: string(eventbus.AiAgentRunCompleted),
	})
	if err != nil {
		return r.failCtx(ctx, err)
	}
	if covered >= r.trigger.SourceEventID {
		return r.end(endedCoalesced)
	}
	started, err := r.e.TaskWork.CountTaskWorkRunsSince(ctx, generated.CountTaskWorkRunsSinceParams{
		WorkspaceID: r.ws, TaskID: nullID(r.trigger.TaskID), AgentID: nullID(r.agentID),
		StartedType: string(eventbus.AiAgentRunStarted),
		Since:       time.Now().UTC().Add(-TaskWorkRateWindow),
	})
	if err != nil {
		return r.failCtx(ctx, err)
	}
	// The count includes this run's own started event.
	if started > TaskWorkMaxRuns {
		return r.end(endedRateLimited)
	}

	prov, err := r.e.Resolver.ForProvider(ctx, r.ws, row.ProviderPublicID)
	if err != nil {
		return r.failCtx(ctx, err)
	}
	if prov == nil {
		return r.fail(endedError, ErrNoProvider)
	}
	r.prov = prov
	return r.moves(ctx, tc)
}

// moves asks for and carries out one move per step.
func (r *taskWorkRun) moves(ctx context.Context, tc taskservice.AgentTaskContext) (agentruntime.ExecutionResult, error) {
	for step := 1; ; step++ {
		if ctx.Err() != nil {
			return r.failCtx(ctx, ctx.Err())
		}
		if step > 1 {
			var stop bool
			var err error
			if tc, stop, err = r.read(ctx); err != nil {
				return r.failCtx(ctx, err)
			}
			if stop {
				return r.end(endedStopped)
			}
		}
		move, ended, err := r.ask(ctx, tc, step)
		if err != nil {
			return r.fail(ended, err)
		}
		if ended != "" {
			return r.end(ended)
		}
		if ended, err := r.carryOut(ctx, tc, step, move); err != nil || ended != "" {
			if err != nil {
				return r.failCtx(ctx, err)
			}
			return r.end(ended)
		}
	}
}

// read loads the task context as the agent sees it and rebuilds the run's
// principal from the current grants. stop is true when the agent may no
// longer work on the task.
func (r *taskWorkRun) read(ctx context.Context) (taskservice.AgentTaskContext, bool, error) {
	reader, err := execution.NewAgentPrincipal(r.ws, r.agentID, []string{execution.ScopeWriteWorkspace}, nil, []types.PublicID{r.task}, 0)
	if err != nil {
		return taskservice.AgentTaskContext{}, false, err
	}
	tc, err := r.e.TaskService.ReadForAgent(ctx, reader, r.task)
	if agentRefused(err) {
		return tc, true, nil
	}
	if err != nil {
		return tc, false, err
	}
	if tc.Task.State == string(generated.TasksDerivedStateDone) ||
		tc.Task.State == string(generated.TasksDerivedStateCancelled) || len(tc.Grants) == 0 {
		return tc, true, nil
	}
	tools := make([]string, 0, len(tc.Grants))
	for tool := range tc.Grants {
		tools = append(tools, string(tool))
	}
	slices.Sort(tools)
	agent, err := execution.NewAgentPrincipal(r.ws, r.agentID, []string{execution.ScopeWriteWorkspace}, tools, []types.PublicID{r.task}, 0)
	if err != nil {
		return tc, false, err
	}
	r.run = taskservice.RunPrincipal{Agent: agent, DelegatorID: r.delegator, TriggerUserID: r.trigger.TriggerUserID}
	r.result.ContextEventPublicID = tc.ContextEventPublicID
	return tc, false, nil
}

// agentRefused reports an authorization refusal that means the agent is no
// longer assigned, enabled or unpaused, or the task is no longer public.
func agentRefused(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *apierrors.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Spec == apierrors.WsTaskNotFound || apiErr.Spec == apierrors.McpScopeInsufficient
	}
	return errors.Is(err, execution.ErrTaskOutsideWorkspace)
}

// ask makes the step's LLM call and its one repair retry. A non-empty
// ended means the run ends before a move was chosen; err is set alongside
// it for the endings the runner treats as failures.
func (r *taskWorkRun) ask(ctx context.Context, tc taskservice.AgentTaskContext, step int) (taskWorkMove, string, error) {
	prompt := taskWorkUserPrompt(tc, r.steps)
	req := airequest.ForAgent(r.prov, airequest.FromExecRow(r.agent), airequest.Args{
		System: taskWorkSystemPrompt(r.agent.SystemPrompt, tc.Grants),
		Prompt: prompt,
		Output: taskWorkOutput,
	})
	callCtx := WithTaskID(ctx, r.trigger.TaskID)
	callCtx = WithInputResources(callCtx, TaskInputResource(r.trigger.TaskPublicID))
	callCtx = WithInvocationAudience(callCtx, InvocationAudienceTask)
	callCtx = providers.WithWorkspaceID(callCtx, r.ws)
	accounting := &Orchestrator{LogInvoke: r.e.Log, OnInvocation: r.e.OnInvocation}

	for attempt := 0; attempt < 2; attempt++ {
		if r.calls >= TaskWorkMaxCalls {
			return taskWorkMove{}, endedMaxSteps, nil
		}
		if ended, err := r.checkBudget(ctx); err != nil {
			return taskWorkMove{}, ended, err
		}
		r.calls++
		var move taskWorkMove
		resp, err := accounting.CompleteAndRecord(callCtx, r.ws, taskWorkPurpose, r.prov, req, func(resp *providers.Response) error {
			m, err := parseTaskWorkMove(resp)
			move = m
			return err
		})
		if resp != nil {
			r.result.CostMicros += resp.EstimatedCostMicros()
			r.result.CostCents += resp.EstimatedCostCents()
		}
		if err == nil {
			return move, "", nil
		}
		if ctx.Err() != nil || !isTaskWorkOutputFailure(err) {
			return taskWorkMove{}, r.errorEnding(ctx), fmt.Errorf("ai: task-work provider call failed: %w", err)
		}
		req = airequest.WithPrompt(req, prompt+"\n\n"+taskWorkRepairReminder)
	}
	r.record(ctx, step, taskWorkMove{}, taskservice.Action{},
		taskservice.Outcome{Result: taskservice.StepInvalid, ErrorCode: endedInvalidOutput})
	return taskWorkMove{}, endedInvalidOutput, nil
}

// checkBudget applies the workspace's daily budget and the agent's monthly
// cap before each call. A cap hit ends the run with budget; a failing read
// ends it with error.
func (r *taskWorkRun) checkBudget(ctx context.Context) (string, error) {
	capHit, err := CheckBudget(ctx, r.e.Guard, r.ws)
	if err == nil {
		err = CheckAgentCostCap(ctx, r.e.Queries, r.agentID, time.Now())
		capHit = errors.Is(err, ErrAgentCostCapExceeded)
	}
	if err == nil {
		return "", nil
	}
	if capHit {
		r.result.CostCapHit = true
		return endedBudget, err
	}
	return r.errorEnding(ctx), err
}

// carryOut applies the run's limits to the chosen move, hands it to
// taskservice and records the step. A non-empty ended ends the run.
func (r *taskWorkRun) carryOut(ctx context.Context, tc taskservice.AgentTaskContext, step int, move taskWorkMove) (string, error) {
	repeat := r.last != nil && move.sameAs(*r.last)
	r.last = &move
	if move.Tool == taskWorkToolFinish {
		summary := deref(move.Args.Summary)
		r.record(ctx, step, move, taskservice.Action{Body: summary}, taskservice.Outcome{Result: taskservice.StepNoop})
		if move.Args.Summary != nil {
			r.result.LastThought = logutil.Redact(stringutil.TruncateBytes(summary, taskWorkMemoBytes))
		}
		return endedDone, nil
	}
	action, ok := move.action(tc.ProjectID)
	switch {
	case repeat:
		r.record(ctx, step, move, action, taskservice.Outcome{Result: taskservice.StepSkipped, ErrorCode: endedRepeat})
		return endedRepeat, nil
	case r.writes >= TaskWorkMaxWrites:
		r.record(ctx, step, move, action, taskservice.Outcome{Result: taskservice.StepSkipped, ErrorCode: endedWriteCap})
		return endedWriteCap, nil
	}

	var out taskservice.Outcome
	switch {
	case !ok:
		out = taskservice.Outcome{Result: taskservice.StepInvalid, ErrorCode: apierrors.ValidationBodyFieldInvalid.Code}
	case move.Tool == execution.ToolCreateSubtask && r.subtasks >= TaskWorkMaxSubtasks:
		out = taskservice.Outcome{Result: taskservice.StepNotPermitted, ErrorCode: taskservice.NotPermittedSubtaskCap}
	default:
		var err error
		r.result.ToolCalls++
		out, err = r.e.TaskService.Execute(ctx, r.run, r.task, action, taskservice.StepRef{RunKey: r.runKey, Step: step})
		if err != nil {
			return "", err
		}
	}
	r.record(ctx, step, move, action, out)

	switch out.Result {
	case taskservice.StepApplied, taskservice.StepProposed:
		r.writes++
		if move.Tool == execution.ToolCreateSubtask {
			r.subtasks++
		}
	case taskservice.StepStopped:
		return endedStopped, nil
	case taskservice.StepFailed:
		r.failures++
		if r.failures >= TaskWorkMaxFailures {
			r.result.ConsecutiveToolFailures = r.failures
			return endedToolError, nil
		}
		return "", nil
	}
	r.failures = 0
	return "", nil
}

// record appends the step to the run's prompt history and writes it as
// agent.task.step. A lost step is logged rather than failing the run: the
// move it describes has already been applied or proposed.
func (r *taskWorkRun) record(ctx context.Context, step int, move taskWorkMove, a taskservice.Action, out taskservice.Outcome) {
	r.steps = append(r.steps, taskWorkStepNote{
		Step: step, Tool: move.Tool, Args: moveArgsText(move), Outcome: out.Result, ErrorCode: out.ErrorCode,
	})
	err := r.e.TaskService.RecordStep(ctx, r.run, r.task, taskservice.StepRef{RunKey: r.runKey, Step: step}, taskservice.StepRecord{
		Tool:    move.Tool,
		Thought: logutil.Redact(move.Thought),
		Action:  a,
		Outcome: out,
	})
	if err != nil {
		slog.ErrorContext(ctx, "ai: task-work step record lost",
			slog.Uint64("workspace_id", uint64(r.ws)), slog.Uint64("agent_id", uint64(r.agentID)),
			slog.String("run_key", r.runKey), slog.Int("step", step), slog.Any("err", err))
	}
}

// errorEnding names a failure: timeout once the run's deadline has passed,
// error otherwise.
func (r *taskWorkRun) errorEnding(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return endedTimeout
	}
	return endedError
}

func (r *taskWorkRun) end(reason string) (agentruntime.ExecutionResult, error) {
	r.result.EndedReason = reason
	return r.result, nil
}

func (r *taskWorkRun) fail(reason string, err error) (agentruntime.ExecutionResult, error) {
	r.result.EndedReason = reason
	return r.result, err
}

func (r *taskWorkRun) failCtx(ctx context.Context, err error) (agentruntime.ExecutionResult, error) {
	return r.fail(r.errorEnding(ctx), err)
}

func nullID(id uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- INT UNSIGNED ids fit int32 within realistic deployments
}
