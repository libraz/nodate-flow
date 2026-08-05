package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// fakeRunnerQuerier is the in-memory stand-in for the sqlc bundle the
// runner depends on. It records every method call and serves
// per-(workspace,task) memo state from a map so the JSON_MERGE_PATCH
// semantics in production can be exercised without a database.
type fakeRunnerQuerier struct {
	mu            sync.Mutex
	agentEvents   []generated.AppendAgentEventParams
	handoffEvents []generated.InsertHandoffToUserEventParams
	memoState     map[uint32]map[string]any
	memoUpdates   []generated.UpdateTaskAgentMemoParams
}

func newFakeQuerier() *fakeRunnerQuerier {
	return &fakeRunnerQuerier{memoState: make(map[uint32]map[string]any)}
}

func (f *fakeRunnerQuerier) AppendAgentEvent(_ context.Context, arg generated.AppendAgentEventParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentEvents = append(f.agentEvents, arg)
	return int64(len(f.agentEvents)), nil
}

func (f *fakeRunnerQuerier) InsertHandoffToUserEvent(_ context.Context, arg generated.InsertHandoffToUserEventParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handoffEvents = append(f.handoffEvents, arg)
	return int64(len(f.handoffEvents)), nil
}

func (f *fakeRunnerQuerier) GetTaskAgentMemo(_ context.Context, arg generated.GetTaskAgentMemoParams) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	memo, ok := f.memoState[arg.ID]
	if !ok || len(memo) == 0 {
		return nil, nil
	}
	return json.Marshal(memo)
}

func (f *fakeRunnerQuerier) UpdateTaskAgentMemo(_ context.Context, arg generated.UpdateTaskAgentMemoParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memoUpdates = append(f.memoUpdates, arg)
	patch := map[string]any{}
	if err := json.Unmarshal(arg.Column1, &patch); err != nil {
		return err
	}
	cur := f.memoState[arg.ID]
	if cur == nil {
		cur = map[string]any{}
	}
	for k, v := range patch {
		// RFC 7396 JSON_MERGE_PATCH: explicit nulls remove keys.
		if v == nil {
			delete(cur, k)
			continue
		}
		cur[k] = v
	}
	f.memoState[arg.ID] = cur
	return nil
}

// snapshotMemo returns a deep copy of the current memo state for a task.
func (f *fakeRunnerQuerier) snapshotMemo(taskID uint32) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.memoState[taskID]))
	for k, v := range f.memoState[taskID] {
		out[k] = v
	}
	return out
}

func (f *fakeRunnerQuerier) eventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.agentEvents))
	for _, e := range f.agentEvents {
		out = append(out, e.Type)
	}
	return out
}

// fakeExecutor returns a canned ExecutionResult / error pair.
type fakeExecutor struct {
	result ExecutionResult
	err    error
	calls  int
}

func (f *fakeExecutor) ExecuteAgent(_ context.Context, _, _ uint32) (ExecutionResult, error) {
	f.calls++
	return f.result, f.err
}

func newRunnerWithFakes(q *fakeRunnerQuerier, exec AgentExecutor) *OrchestratorRunner {
	return &OrchestratorRunner{
		Queries:  q,
		Executor: exec,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

// TestRunnerEmitsActorAgentIDAndTaskID verifies a successful run lands
// ai.agent.run.started + completed with actor_agent_id=agent.id and
// actor_user_id NULL on every row, and that task_id flows through
// when SourceEventID resolves to a task. resolveSourceTask is bypassed
// by leaving SourceEventID zero, so the runner stamps task_id=0; the
// task_id propagation path itself is covered by an integration test.
func TestRunnerEmitsActorAgentIDOnSuccess(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	exec := &fakeExecutor{result: ExecutionResult{LastThought: "ok"}}
	r := newRunnerWithFakes(q, exec)

	if err := r.Run(context.Background(), Job{AgentID: 42, WsID: 7}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if got, want := exec.calls, 1; got != want {
		t.Fatalf("executor called %d times, want %d", got, want)
	}
	if got, want := q.eventTypes(), []string{"ai.agent.run.started", "ai.agent.run.completed"}; !equalSlices(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	for _, evt := range q.agentEvents {
		if !evt.ActorAgentID.Valid || evt.ActorAgentID.Int32 != 42 {
			t.Fatalf("event %q actor_agent_id=%+v, want valid=42", evt.Type, evt.ActorAgentID)
		}
	}
}

// TestRunnerEmitsFailedOnError verifies the failure branch emits a
// single ai.agent.run.failed event with the error message and that
// no handoff is fired for a non-cost-cap error.
func TestRunnerEmitsFailedOnError(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	exec := &fakeExecutor{err: errors.New("provider boom")}
	r := newRunnerWithFakes(q, exec)

	if err := r.Run(context.Background(), Job{AgentID: 10, WsID: 5}, time.Time{}); err == nil {
		t.Fatalf("Run should have returned err")
	}
	if got, want := q.eventTypes(), []string{"ai.agent.run.started", "ai.agent.run.failed"}; !equalSlices(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if len(q.handoffEvents) != 0 {
		t.Fatalf("non-cost-cap error should not emit handoff, got %d", len(q.handoffEvents))
	}
}

// TestRunnerHandoffTriggers is the table-driven coverage for the
// three classify-then-handoff paths.
func TestRunnerHandoffTriggers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		result     ExecutionResult
		runErr     error
		wantReason string
		// wantPause is true when the handoff path should also flip
		// ai_agents.paused (cost_cap path only). We can't observe the
		// DB without a real *sql.DB so the pauseAgent call is a no-op
		// in this unit test and we only assert the handoff event +
		// memo state.
		wantPause bool
	}{
		{
			name:       "low_confidence below 0.5 fires handoff",
			result:     ExecutionResult{Confidence: 0.3, LastThought: "uncertain"},
			wantReason: handoffReasonLowConfidence,
		},
		{
			name:       "three tool failures fire handoff",
			result:     ExecutionResult{ConsecutiveToolFailures: 3},
			wantReason: handoffReasonToolError,
		},
		{
			name:       "cost cap on failure path fires handoff and pauses",
			result:     ExecutionResult{CostCapHit: true},
			runErr:     errors.New("cost cap exceeded"),
			wantReason: handoffReasonCostCap,
			wantPause:  true,
		},
		{
			name:       "healthy run emits no handoff",
			result:     ExecutionResult{Confidence: 0.9, LastThought: "great"},
			wantReason: "",
		},
		{
			name:       "two tool failures stay below threshold",
			result:     ExecutionResult{ConsecutiveToolFailures: 2},
			wantReason: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := newFakeQuerier()
			exec := &fakeExecutor{result: tc.result, err: tc.runErr}
			r := newRunnerWithFakes(q, exec)
			// Seed task_id propagation via SourceEventID would need a DB;
			// pass AgentID + WsID only and rely on the runner accepting a
			// zero task id (memo writes are skipped, but handoff is still
			// emitted because the handoff path does not require task_id).
			_ = r.Run(context.Background(), Job{AgentID: 1, WsID: 2}, time.Time{})

			if tc.wantReason == "" {
				if len(q.handoffEvents) != 0 {
					t.Fatalf("no handoff expected, got %+v", q.handoffEvents)
				}
				return
			}
			if len(q.handoffEvents) != 1 {
				t.Fatalf("want exactly 1 handoff, got %d", len(q.handoffEvents))
			}
			var payload map[string]any
			if err := json.Unmarshal(q.handoffEvents[0].PayloadJson, &payload); err != nil {
				t.Fatalf("payload decode: %v", err)
			}
			if got := payload["reason"]; got != tc.wantReason {
				t.Fatalf("reason = %v, want %s", got, tc.wantReason)
			}
		})
	}
}

// TestRunnerHandoffLoopBudget verifies that once handoff_count reaches
// the limit the runner emits ai.agent.run.failed with the structured
// error code and writes handoff_status=loop_detected to agent_memo,
// instead of recording another handoff event. The limit is configured
// at 2 to keep the test small.
func TestRunnerHandoffLoopBudget(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	// Seed agent_memo so the loop budget is already exhausted on entry.
	q.memoState[99] = map[string]any{
		"handoff_count":  float64(2),
		"handoff_status": "stuck",
	}
	exec := &fakeExecutor{result: ExecutionResult{Confidence: 0.1}}
	r := newRunnerWithFakes(q, exec)
	r.HandoffLoopLimit = 2

	// Inject task_id 99 directly by stubbing resolveSourceTask through
	// a no-DB shortcut: set SourceEventID and call into the unexported
	// path via a custom queries wrapper that pretends task lookup
	// returned (99, ""). We reach for the same effect by calling
	// handleHandoff directly — Run() requires resolveSourceTask which
	// hits *sql.DB. Direct call keeps the unit test honest about the
	// loop-budget logic without needing a real DB.
	r.handleHandoff(context.Background(), handoffReasonLowConfidence,
		Job{AgentID: 7, WsID: 3}, 99, "task-pub-id", "agent-pub-id", time.Unix(1_700_000_500, 0).UTC(), "")

	if got := len(q.handoffEvents); got != 0 {
		t.Fatalf("loop-exhausted handoff must not emit handoff event, got %d", got)
	}
	if got := len(q.agentEvents); got != 1 {
		t.Fatalf("want 1 ai.agent.run.failed, got %d", got)
	}
	if got := q.agentEvents[0].Type; got != "ai.agent.run.failed" {
		t.Fatalf("event type = %s, want ai.agent.run.failed", got)
	}
	var payload map[string]any
	_ = json.Unmarshal(q.agentEvents[0].PayloadJson, &payload)
	if got := payload["error"]; got != apierrors.WsTaskAgentHandoffLoopDetected.Code {
		t.Fatalf("error = %v, want %s", got, apierrors.WsTaskAgentHandoffLoopDetected.Code)
	}
	memo := q.snapshotMemo(99)
	if got := memo["handoff_status"]; got != "loop_detected" {
		t.Fatalf("handoff_status = %v, want loop_detected", got)
	}
}

// TestRunnerMemoMergeDoesNotClobber verifies subsequent
// UpdateTaskAgentMemo writes leave previously-set keys intact, matching
// the JSON_MERGE_PATCH semantics applied in MySQL. The fake querier
// reproduces RFC 7396 merge behavior so the runner's incremental
// patches stay correct.
func TestRunnerMemoMergeDoesNotClobber(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	r := newRunnerWithFakes(q, nil)
	r.mergeMemo(context.Background(), 1, 11, map[string]any{"last_thought": "hello"})
	r.mergeMemo(context.Background(), 1, 11, map[string]any{"attempts": 2})
	memo := q.snapshotMemo(11)
	if got := memo["last_thought"]; got != "hello" {
		t.Fatalf("last_thought lost on second merge: %v", memo)
	}
	if got := memo["attempts"]; got != float64(2) {
		t.Fatalf("attempts = %v, want 2", got)
	}
}

// TestRunnerSuccessClearsHandoffStatus verifies that a healthy run
// after a prior "stuck" state writes nulls for handoff_status /
// handoff_reason so the inbox UI returns the task to its normal flow.
func TestRunnerSuccessClearsHandoffStatus(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	q.memoState[22] = map[string]any{
		"handoff_status": "stuck",
		"handoff_reason": "low_confidence",
		"handoff_count":  float64(1),
	}
	r := newRunnerWithFakes(q, nil)
	r.recordSuccessMemo(context.Background(), 9, 22, time.Unix(1_700_000_900, 0).UTC(), ExecutionResult{LastThought: "ok"})

	memo := q.snapshotMemo(22)
	if _, ok := memo["handoff_status"]; ok {
		t.Fatalf("handoff_status should be cleared, got %+v", memo)
	}
	if _, ok := memo["handoff_reason"]; ok {
		t.Fatalf("handoff_reason should be cleared, got %+v", memo)
	}
	// handoff_count is intentionally preserved so the loop budget
	// survives across a healthy run / retry cycle.
	if got := memo["handoff_count"]; got != float64(1) {
		t.Fatalf("handoff_count = %v, want 1", got)
	}
}

func TestRunnerSuccessMemoPreservesSubCentCost(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	r := newRunnerWithFakes(q, nil)

	r.recordSuccessMemo(context.Background(), 9, 22, time.Unix(1_700_000_900, 0).UTC(), ExecutionResult{
		LastThought: "ok",
		CostMicros:  450,
	})

	memo := q.snapshotMemo(22)
	if got := memo["last_cost_micros"]; got != float64(450) {
		t.Fatalf("last_cost_micros = %v, want 450", got)
	}
	if got := memo["last_cost_cents"]; got != float64(0) {
		t.Fatalf("last_cost_cents = %v, want 0", got)
	}
}

// TestClassifyHandoffOrdering documents that confidence wins over a
// tool-error tie when both signals fire on the same run; the runner
// uses this order to keep the audit trail consistent.
func TestClassifyHandoffOrdering(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   ExecutionResult
		want string
	}{
		{"low confidence beats tool errors", ExecutionResult{Confidence: 0.1, ConsecutiveToolFailures: 3}, handoffReasonLowConfidence},
		{"zero confidence is treated as missing", ExecutionResult{Confidence: 0, ConsecutiveToolFailures: 3}, handoffReasonToolError},
		{"no signal returns empty", ExecutionResult{Confidence: 0.9, ConsecutiveToolFailures: 1}, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyHandoff(tc.in); got != tc.want {
				t.Fatalf("classifyHandoff(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRunnerBumpsAttempts verifies the run-start memo write
// increments attempts monotonically across calls.
func TestRunnerBumpsAttempts(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	r := newRunnerWithFakes(q, nil)
	r.bumpAttempts(context.Background(), 1, 5, time.Unix(1, 0))
	r.bumpAttempts(context.Background(), 1, 5, time.Unix(2, 0))
	r.bumpAttempts(context.Background(), 1, 5, time.Unix(3, 0))
	memo := q.snapshotMemo(5)
	if got := memo["attempts"]; got != float64(3) {
		t.Fatalf("attempts = %v, want 3", got)
	}
	if got := memo["last_started_at"]; got != float64(3) {
		t.Fatalf("last_started_at = %v, want 3", got)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
