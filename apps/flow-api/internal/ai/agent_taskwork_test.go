package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

const (
	twWorkspace   uint32 = 42
	twAgent       uint32 = 7
	twTaskID      uint32 = 11
	twProject     uint32 = 3
	twDelegator   int32  = 9
	twTriggerUser uint32 = 5
	twSourceEvent int64  = 100
	twContextEvt  int64  = 120
	twCallMicros  int64  = 1_000
)

// twExecQueries is the agent row and the monthly cap a task-work run reads.
// spent is consulted on every cap check so a test can raise it mid-run.
type twExecQueries struct {
	row      generated.GetAgentForExecRow
	capCents *int32
	spent    func() int64
}

func (q *twExecQueries) GetAgentForExec(context.Context, generated.GetAgentForExecParams) (generated.GetAgentForExecRow, error) {
	return q.row, nil
}

func (q *twExecQueries) GetAgentGuardSnapshot(context.Context, uint32) (generated.GetAgentGuardSnapshotRow, error) {
	row := generated.GetAgentGuardSnapshotRow{Enabled: true}
	if q.capCents != nil {
		row.MonthlyCostCapCents = sql.NullInt32{Int32: *q.capCents, Valid: true}
	}
	return row, nil
}

func (q *twExecQueries) SumAiCostForAgentSince(context.Context, generated.SumAiCostForAgentSinceParams) (int64, error) {
	if q.spent == nil {
		return 0, nil
	}
	return q.spent(), nil
}

// twStartQueries answers the start-of-run checks.
type twStartQueries struct {
	delegator    sql.NullInt32
	delegatorErr error
	covered      int64
	started      int64
	countArg     generated.CountTaskWorkRunsSinceParams
	coalesceArg  generated.GetLatestTaskWorkContextEventIDParams
}

func (q *twStartQueries) GetAssigneeDelegator(context.Context, generated.GetAssigneeDelegatorParams) (sql.NullInt32, error) {
	return q.delegator, q.delegatorErr
}

func (q *twStartQueries) GetLatestTaskWorkContextEventID(_ context.Context, arg generated.GetLatestTaskWorkContextEventIDParams) (int64, error) {
	q.coalesceArg = arg
	return q.covered, nil
}

func (q *twStartQueries) CountTaskWorkRunsSince(_ context.Context, arg generated.CountTaskWorkRunsSinceParams) (int64, error) {
	q.countArg = arg
	return q.started, nil
}

type twStep struct {
	ref taskservice.StepRef
	rec taskservice.StepRecord
}

// twService stands in for taskservice. read answers each ReadForAgent by
// call number (1-based); execute decides each Execute.
type twService struct {
	mu       sync.Mutex
	read     func(n int) (taskservice.AgentTaskContext, error)
	reads    int
	execute  func(a taskservice.Action) (taskservice.Outcome, error)
	executed []taskservice.Action
	runs     []taskservice.RunPrincipal
	refs     []taskservice.StepRef
	steps    []twStep
}

func (s *twService) ReadForAgent(context.Context, execution.Principal, types.PublicID) (taskservice.AgentTaskContext, error) {
	s.mu.Lock()
	s.reads++
	n := s.reads
	s.mu.Unlock()
	if s.read != nil {
		return s.read(n)
	}
	return twContext(), nil
}

func (s *twService) Execute(_ context.Context, run taskservice.RunPrincipal, _ types.PublicID, a taskservice.Action, step taskservice.StepRef) (taskservice.Outcome, error) {
	s.mu.Lock()
	s.executed = append(s.executed, a)
	s.runs = append(s.runs, run)
	s.refs = append(s.refs, step)
	s.mu.Unlock()
	if s.execute != nil {
		return s.execute(a)
	}
	return twGrantOutcome(a), nil
}

func (s *twService) RecordStep(_ context.Context, _ taskservice.RunPrincipal, _ types.PublicID, step taskservice.StepRef, rec taskservice.StepRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, twStep{ref: step, rec: rec})
	return nil
}

func (s *twService) outcomes() []taskservice.StepOutcome {
	out := make([]taskservice.StepOutcome, len(s.steps))
	for i, st := range s.steps {
		out[i] = st.rec.Outcome.Result
	}
	return out
}

var twGrants = execution.ToolGrants{
	execution.ToolAddComment:     execution.GrantModeAuto,
	execution.ToolSetPriority:    execution.GrantModeApproval,
	execution.ToolSetDueOn:       execution.GrantModeAuto,
	execution.ToolTransitionTask: execution.GrantModeAuto,
	execution.ToolCreateSubtask:  execution.GrantModeAuto,
}

var twContextPublic = types.New()

func twContext() taskservice.AgentTaskContext {
	return taskservice.AgentTaskContext{
		Task: taskservice.AgentTask{
			Title: "Write the release notes", State: "in_progress", Priority: 2,
			CompletionPolicy: generated.TasksCompletionPolicyReviewRequired,
		},
		ProjectID:            twProject,
		ContextEventID:       twContextEvt,
		ContextEventPublicID: twContextPublic,
		Grants:               twGrants,
	}
}

// twGrantOutcome applies auto-granted tools and proposes approval ones.
func twGrantOutcome(a taskservice.Action) taskservice.Outcome {
	if twGrants[a.Kind] == execution.GrantModeApproval {
		return taskservice.Outcome{Result: taskservice.StepProposed, ProposalID: types.New()}
	}
	return taskservice.Outcome{Result: taskservice.StepApplied}
}

// twMove renders one model answer with every argument present.
func twMove(tool string, args map[string]any) string {
	full := map[string]any{"body": nil, "priority": nil, "due_on": nil, "transition": nil, "title": nil, "summary": nil}
	for k, v := range args {
		full[k] = v
	}
	raw, err := json.Marshal(map[string]any{"thought": "next step", "tool": tool, "args": full})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func twComment(body string) string {
	return twMove(execution.ToolAddComment, map[string]any{"body": body})
}

func twFinish(summary any) string {
	return twMove(taskWorkToolFinish, map[string]any{"summary": summary})
}

// twProvider answers from a script, one entry per call; past its end it
// finishes. Every call costs twCallMicros.
type twProvider struct {
	mu      sync.Mutex
	script  []string
	calls   int
	prompts []string
	reqs    []providers.Request
	block   bool
}

func (*twProvider) Name() string         { return "stub" }
func (*twProvider) Kind() providers.Kind { return providers.KindOllama }
func (*twProvider) Model() string        { return "stub-default" }
func (p *twProvider) Complete(ctx context.Context, req providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.prompts = append(p.prompts, req.Prompt)
	p.reqs = append(p.reqs, req)
	text := twFinish(nil)
	if n <= len(p.script) {
		text = p.script[n-1]
	}
	p.mu.Unlock()
	if p.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &providers.Response{Model: req.Model, Text: text, Termination: providers.TerminationComplete, CostMicros: twCallMicros}, nil
}

type twHarness struct {
	exec     *AgentExecutor
	prov     *twProvider
	svc      *twService
	start    *twStartQueries
	agent    *twExecQueries
	recorded []InvocationRecord
	task     types.PublicID
}

func newTaskWorkHarness(script ...string) *twHarness {
	h := &twHarness{
		prov:  &twProvider{script: script},
		svc:   &twService{},
		start: &twStartQueries{delegator: sql.NullInt32{Int32: twDelegator, Valid: true}},
		agent: &twExecQueries{row: generated.GetAgentForExecRow{
			ID: twAgent, WorkspaceID: twWorkspace, Name: "writer", SystemPrompt: "You write release notes.",
			Temperature: 70, ModelName: "stub-model",
		}},
		task: types.New(),
	}
	h.exec = &AgentExecutor{
		Queries: h.agent,
		Resolver: AgentProviderResolverFunc(func(context.Context, uint32, types.PublicID) (providers.Provider, error) {
			return h.prov, nil
		}),
		Log:         func(_ context.Context, rec InvocationRecord) { h.recorded = append(h.recorded, rec) },
		TaskService: h.svc,
		TaskWork:    h.start,
	}
	return h
}

func (h *twHarness) run(t *testing.T) (agentruntime.ExecutionResult, error) {
	t.Helper()
	ctx := agentruntime.WithTrigger(context.Background(), agentruntime.Trigger{
		EventType:     string(eventbus.TaskCommentAdded),
		TaskPublicID:  h.task.String(),
		TaskID:        twTaskID,
		SourceEventID: twSourceEvent,
		TriggerUserID: twTriggerUser,
		TaskWork:      true,
	})
	ctx = agentruntime.WithRunKey(ctx, types.New().String())
	return h.exec.ExecuteAgent(ctx, twWorkspace, twAgent)
}

func (h *twHarness) mustEnd(t *testing.T, want string) agentruntime.ExecutionResult {
	t.Helper()
	res, err := h.run(t)
	if err != nil {
		t.Fatalf("ExecuteAgent: %v", err)
	}
	if res.EndedReason != want {
		t.Fatalf("EndedReason = %q, want %q (steps %v)", res.EndedReason, want, h.svc.outcomes())
	}
	return res
}

func TestTaskWorkLoop_Coalesced(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness(twComment("hello"))
	h.start.covered = twSourceEvent
	h.mustEnd(t, endedCoalesced)
	if h.prov.calls != 0 || len(h.svc.executed) != 0 || len(h.svc.steps) != 0 {
		t.Fatalf("coalesced run made %d calls, %d executes, %d steps; want none", h.prov.calls, len(h.svc.executed), len(h.svc.steps))
	}
	if h.start.coalesceArg.CompletedType != string(eventbus.AiAgentRunCompleted) || h.start.coalesceArg.TaskID.Int32 != int32(twTaskID) {
		t.Fatalf("coalescing query args = %+v", h.start.coalesceArg)
	}

	// A source event newer than every completed run's context is not covered.
	h = newTaskWorkHarness(twFinish(nil))
	h.start.covered = twSourceEvent - 1
	h.mustEnd(t, endedDone)
	if h.prov.calls != 1 {
		t.Fatalf("uncovered run made %d calls, want 1", h.prov.calls)
	}
}

func TestTaskWorkLoop_RateLimited(t *testing.T) {
	t.Parallel()

	// The count includes this run's own started event.
	h := newTaskWorkHarness(twFinish(nil))
	h.start.started = TaskWorkMaxRuns + 1
	before := time.Now().UTC()
	h.mustEnd(t, endedRateLimited)
	if h.prov.calls != 0 {
		t.Fatalf("rate-limited run made %d calls, want 0", h.prov.calls)
	}
	arg := h.start.countArg
	if arg.StartedType != string(eventbus.AiAgentRunStarted) {
		t.Fatalf("StartedType = %q", arg.StartedType)
	}
	if d := before.Sub(arg.Since); d < TaskWorkRateWindow-time.Minute || d > TaskWorkRateWindow+time.Minute {
		t.Fatalf("Since is %v before now, want %v", d, TaskWorkRateWindow)
	}

	h = newTaskWorkHarness(twFinish(nil))
	h.start.started = TaskWorkMaxRuns
	h.mustEnd(t, endedDone)
}

func TestTaskWorkLoop_OutcomesReachRunRecord(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness(
		twComment("Drafted the notes."),
		twMove(execution.ToolSetPriority, map[string]any{"priority": 1}),
		twFinish("Commented and proposed a priority change."),
	)
	res := h.mustEnd(t, endedDone)

	got := h.svc.outcomes()
	want := []taskservice.StepOutcome{taskservice.StepApplied, taskservice.StepProposed, taskservice.StepNoop}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("step outcomes = %v, want %v", got, want)
	}
	if h.svc.steps[1].rec.Outcome.ProposalID == (types.PublicID{}) {
		t.Fatal("proposed step carries no proposal id")
	}
	if a := h.svc.executed[1]; a.Kind != execution.ToolSetPriority || a.Priority != 1 {
		t.Fatalf("second action = %+v", a)
	}
	run := h.svc.runs[0]
	if run.DelegatorID != uint32(twDelegator) || run.TriggerUserID != twTriggerUser {
		t.Fatalf("run principal delegator=%d trigger=%d", run.DelegatorID, run.TriggerUserID)
	}
	if !run.Agent.HasTool(execution.ToolAddComment) || !run.Agent.HasTool(execution.ToolSetPriority) || run.Agent.AgentID() != twAgent {
		t.Fatalf("run principal tools = %v", run.Agent.Tools())
	}
	if res.ToolCalls != 2 || res.ContextEventPublicID != twContextPublic || res.CostMicros != 3*twCallMicros {
		t.Fatalf("result = %+v", res)
	}
	if want := "Completion policy: " + string(generated.TasksCompletionPolicyReviewRequired); !strings.Contains(h.prov.prompts[0], want) {
		t.Fatalf("prompt lacks %q", want)
	}
	if res.LastThought != "Commented and proposed a priority change." {
		t.Fatalf("LastThought = %q", res.LastThought)
	}

	// A null summary leaves the memo alone.
	h = newTaskWorkHarness(twFinish(nil))
	if res := h.mustEnd(t, endedDone); res.LastThought != "" {
		t.Fatalf("LastThought = %q, want empty", res.LastThought)
	}
}

func TestTaskWorkLoop_RecordsEveryStep(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness(
		twComment("one"),
		twMove(execution.ToolSetDueOn, map[string]any{"due_on": "not-a-date"}),
		twMove(execution.ToolTransitionTask, map[string]any{"transition": "start"}),
		twFinish("ok"),
	)
	h.mustEnd(t, endedDone)
	if len(h.svc.steps) != 4 {
		t.Fatalf("recorded %d steps, want 4", len(h.svc.steps))
	}
	key := h.svc.steps[0].ref.RunKey
	for i, st := range h.svc.steps {
		if st.ref.Step != i+1 || st.ref.RunKey != key || key == "" {
			t.Fatalf("step %d ref = %+v", i, st.ref)
		}
	}
	if st := h.svc.steps[1].rec; st.Outcome.Result != taskservice.StepInvalid || st.Outcome.ErrorCode != apierrors.ValidationBodyFieldInvalid.Code {
		t.Fatalf("unparseable due date step = %+v", st.Outcome)
	}
	if len(h.svc.executed) != 2 {
		t.Fatalf("executed %d actions, want 2 (the invalid one is not executed)", len(h.svc.executed))
	}
	for i, ref := range h.svc.refs {
		if ref.RunKey != key {
			t.Fatalf("execute %d run key %q, want %q", i, ref.RunKey, key)
		}
	}
}

func TestTaskWorkLoop_Repeat(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness(twComment("same"), twComment("same"), twFinish(nil))
	h.mustEnd(t, endedRepeat)
	if len(h.svc.executed) != 1 {
		t.Fatalf("executed %d actions, want 1", len(h.svc.executed))
	}
	if st := h.svc.steps[1].rec.Outcome; st.Result != taskservice.StepSkipped || st.ErrorCode != endedRepeat {
		t.Fatalf("repeated step = %+v", st)
	}
}

func TestTaskWorkLoop_MaxStepsCountsRepair(t *testing.T) {
	t.Parallel()

	script := []string{"not json"}
	for i := 1; i <= 10; i++ {
		script = append(script, twComment(fmt.Sprintf("note %d", i)))
	}
	h := newTaskWorkHarness(script...)
	h.svc.execute = func(taskservice.Action) (taskservice.Outcome, error) {
		return taskservice.Outcome{Result: taskservice.StepNoop}, nil
	}
	h.mustEnd(t, endedMaxSteps)
	if h.prov.calls != TaskWorkMaxCalls {
		t.Fatalf("provider calls = %d, want %d", h.prov.calls, TaskWorkMaxCalls)
	}
	if !strings.Contains(h.prov.prompts[1], taskWorkRepairReminder) {
		t.Fatal("repair retry does not carry the reminder")
	}
	if len(h.svc.steps) != TaskWorkMaxCalls-1 {
		t.Fatalf("recorded %d steps, want %d", len(h.svc.steps), TaskWorkMaxCalls-1)
	}
}

func TestTaskWorkLoop_WriteCap(t *testing.T) {
	t.Parallel()

	var script []string
	for i := 1; i <= 8; i++ {
		script = append(script, twComment(fmt.Sprintf("note %d", i)))
	}
	h := newTaskWorkHarness(script...)
	h.mustEnd(t, endedWriteCap)
	if len(h.svc.executed) != TaskWorkMaxWrites {
		t.Fatalf("executed %d actions, want %d", len(h.svc.executed), TaskWorkMaxWrites)
	}
	last := h.svc.steps[len(h.svc.steps)-1].rec.Outcome
	if len(h.svc.steps) != TaskWorkMaxWrites+1 || last.Result != taskservice.StepSkipped || last.ErrorCode != endedWriteCap {
		t.Fatalf("steps = %v, last %+v", h.svc.outcomes(), last)
	}
}

func TestTaskWorkLoop_SubtaskCapPerRun(t *testing.T) {
	t.Parallel()

	var script []string
	for i := 1; i <= TaskWorkMaxSubtasks+1; i++ {
		script = append(script, twMove(execution.ToolCreateSubtask, map[string]any{"title": fmt.Sprintf("part %d", i)}))
	}
	script = append(script, twFinish(nil))
	h := newTaskWorkHarness(script...)
	h.mustEnd(t, endedDone)
	if len(h.svc.executed) != TaskWorkMaxSubtasks {
		t.Fatalf("executed %d subtasks, want %d", len(h.svc.executed), TaskWorkMaxSubtasks)
	}
	for _, a := range h.svc.executed {
		if a.ProjectID != twProject || a.Title == "" {
			t.Fatalf("subtask action = %+v", a)
		}
	}
	capped := h.svc.steps[TaskWorkMaxSubtasks].rec.Outcome
	if capped.Result != taskservice.StepNotPermitted || capped.ErrorCode != taskservice.NotPermittedSubtaskCap {
		t.Fatalf("subtask over the cap = %+v", capped)
	}
}

func TestTaskWorkLoop_Budget(t *testing.T) {
	t.Parallel()

	t.Run("monthly agent cap", func(t *testing.T) {
		t.Parallel()
		capCents := int32(1_000)
		h := newTaskWorkHarness(twComment("one"), twComment("two"), twComment("three"))
		h.agent.capCents = &capCents
		h.agent.spent = func() int64 {
			if h.prov.calls >= 1 {
				return 1_000
			}
			return 0
		}
		res, err := h.run(t)
		if !errors.Is(err, ErrAgentCostCapExceeded) || !res.CostCapHit || res.EndedReason != endedBudget {
			t.Fatalf("result = %+v, err = %v; want a budget ending with CostCapHit", res, err)
		}
		if h.prov.calls != 1 {
			t.Fatalf("provider calls = %d, want 1", h.prov.calls)
		}
	})

	t.Run("daily workspace budget", func(t *testing.T) {
		t.Parallel()
		h := newTaskWorkHarness(twComment("one"), twComment("two"))
		h.exec.Guard = NewCostGuard(BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
			if h.prov.calls >= 1 {
				return 100, nil
			}
			return 0, nil
		}), 100)
		res, err := h.run(t)
		if !errors.Is(err, ErrDailyBudgetExceeded) || !res.CostCapHit || res.EndedReason != endedBudget {
			t.Fatalf("result = %+v, err = %v; want a budget ending with CostCapHit", res, err)
		}
		if h.prov.calls != 1 {
			t.Fatalf("provider calls = %d, want 1", h.prov.calls)
		}
	})
}

func TestTaskWorkLoop_Stopped(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		setup     func(h *twHarness)
		wantCalls int
	}{
		{"execute finds the agent unassigned", func(h *twHarness) {
			h.svc.execute = func(taskservice.Action) (taskservice.Outcome, error) {
				return taskservice.Outcome{Result: taskservice.StepStopped}, nil
			}
		}, 1},
		{"agent paused", func(h *twHarness) { h.agent.row.Paused = true }, 0},
		{"no enabled assignment", func(h *twHarness) { h.start.delegatorErr = sql.ErrNoRows }, 0},
		{"task no longer readable", func(h *twHarness) {
			h.svc.read = func(int) (taskservice.AgentTaskContext, error) {
				return taskservice.AgentTaskContext{}, &apierrors.APIError{Spec: apierrors.WsTaskNotFound}
			}
		}, 0},
		{"task done", func(h *twHarness) {
			h.svc.read = func(int) (taskservice.AgentTaskContext, error) {
				c := twContext()
				c.Task.State = string(generated.TasksDerivedStateDone)
				return c, nil
			}
		}, 0},
		{"grants revoked mid-run", func(h *twHarness) {
			h.svc.read = func(n int) (taskservice.AgentTaskContext, error) {
				c := twContext()
				if n > 1 {
					c.Grants = execution.ToolGrants{}
				}
				return c, nil
			}
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTaskWorkHarness(twComment("one"), twComment("two"))
			tc.setup(h)
			h.mustEnd(t, endedStopped)
			if h.prov.calls != tc.wantCalls {
				t.Fatalf("provider calls = %d, want %d", h.prov.calls, tc.wantCalls)
			}
		})
	}
}

func TestTaskWorkLoop_ToolError(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness(twComment("a"), twComment("b"), twComment("c"), twComment("d"))
	h.svc.execute = func(taskservice.Action) (taskservice.Outcome, error) {
		return taskservice.Outcome{Result: taskservice.StepFailed}, nil
	}
	res := h.mustEnd(t, endedToolError)
	if res.ConsecutiveToolFailures != TaskWorkMaxFailures || len(h.svc.executed) != TaskWorkMaxFailures {
		t.Fatalf("failures = %d, executed = %d", res.ConsecutiveToolFailures, len(h.svc.executed))
	}
}

func TestTaskWorkLoop_InvalidOutput(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness("I think I will comment.", `{"tool":"delete_task"}`)
	h.mustEnd(t, endedInvalidOutput)
	if h.prov.calls != 2 || len(h.svc.executed) != 0 {
		t.Fatalf("calls = %d, executed = %d", h.prov.calls, len(h.svc.executed))
	}
	if got := h.svc.outcomes(); len(got) != 1 || got[0] != taskservice.StepInvalid {
		t.Fatalf("steps = %v, want one invalid", got)
	}
}

func TestTaskWorkLoop_Timeout(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness()
	h.prov.block = true
	h.exec.taskWorkTimeout = 20 * time.Millisecond
	res, err := h.run(t)
	if err == nil || res.EndedReason != endedTimeout {
		t.Fatalf("result = %+v, err = %v; want a timeout failure", res, err)
	}
}

func TestTaskWorkLoop_InvocationContext(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness(twComment("one"), twFinish(nil))
	h.mustEnd(t, endedDone)
	if len(h.recorded) != 2 {
		t.Fatalf("logged %d invocations, want 2", len(h.recorded))
	}
	for _, rec := range h.recorded {
		if rec.Purpose != taskWorkPurpose || rec.AgentID != twAgent || rec.TaskID != twTaskID || rec.Audience != InvocationAudienceTask {
			t.Fatalf("invocation record = %+v", rec)
		}
		if len(rec.InputResources) != 1 || rec.InputResources[0] != TaskInputResource(h.task.String()) {
			t.Fatalf("input resources = %+v", rec.InputResources)
		}
	}
	if req := h.prov.reqs[0]; req.Output == nil || req.Output.Contract() != taskWorkPurpose || req.Model != "stub-model" {
		t.Fatalf("request model=%q output=%v", req.Model, req.Output)
	}
}

func TestTaskWorkLoop_UnwiredExecutorFails(t *testing.T) {
	t.Parallel()

	h := newTaskWorkHarness()
	h.exec.TaskService = nil
	if _, err := h.run(t); !errors.Is(err, ErrTaskWorkUnavailable) {
		t.Fatalf("err = %v, want ErrTaskWorkUnavailable", err)
	}
}
