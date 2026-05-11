package autoactions

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// fakeHandoffQuerier is an in-memory stand-in for the sqlc bundle used
// by the handoff_to_user code path. Behaviour mirrors the runtime's
// fakeRunnerQuerier: agent_memo is stored as a map<taskID, map[string]any>
// and UpdateTaskAgentMemo applies RFC 7396 merge semantics so the
// JSON_MERGE_PATCH behaviour from MySQL is reproduced faithfully.
type fakeHandoffQuerier struct {
	mu            sync.Mutex
	handoffEvents []generated.InsertHandoffToUserEventParams
	memoState     map[uint32]map[string]any
	memoUpdates   []generated.UpdateTaskAgentMemoParams
}

func newFakeHandoffQuerier() *fakeHandoffQuerier {
	return &fakeHandoffQuerier{memoState: make(map[uint32]map[string]any)}
}

func (f *fakeHandoffQuerier) InsertHandoffToUserEvent(_ context.Context, arg generated.InsertHandoffToUserEventParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handoffEvents = append(f.handoffEvents, arg)
	return int64(len(f.handoffEvents)), nil
}

func (f *fakeHandoffQuerier) GetTaskAgentMemo(_ context.Context, arg generated.GetTaskAgentMemoParams) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	memo, ok := f.memoState[arg.ID]
	if !ok || len(memo) == 0 {
		return nil, nil
	}
	return json.Marshal(memo)
}

func (f *fakeHandoffQuerier) UpdateTaskAgentMemo(_ context.Context, arg generated.UpdateTaskAgentMemoParams) error {
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
		if v == nil {
			delete(cur, k)
			continue
		}
		cur[k] = v
	}
	f.memoState[arg.ID] = cur
	return nil
}

func (f *fakeHandoffQuerier) snapshotMemo(taskID uint32) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.memoState[taskID]))
	for k, v := range f.memoState[taskID] {
		out[k] = v
	}
	return out
}

// fakeActorDisabler records DisableAgentActor calls so tests can
// assert the executor disabled the correct (workspace, task, agent)
// triple. err can be set to verify the executor surfaces failures.
type fakeActorDisabler struct {
	mu    sync.Mutex
	calls []disableCall
	err   error
}

type disableCall struct {
	workspaceID uint32
	taskID      uint32
	agentID     uint32
}

func (f *fakeActorDisabler) DisableAgentActor(_ context.Context, workspaceID, taskID, agentID uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, disableCall{workspaceID: workspaceID, taskID: taskID, agentID: agentID})
	return f.err
}

// makeTaskRow builds a taskRow fixture with an agent assignee already
// attached so applyHandoffToUser does not short-circuit on the
// defensive guard.
func makeTaskRow(t *testing.T) taskRow {
	t.Helper()
	return taskRow{
		id:               42,
		publicID:         types.New(),
		workspaceID:      7,
		title:            "stuck task",
		derivedState:     string(StateOpen),
		hasAgentAssignee: true,
		agentID:          99,
		agentPublicID:    types.New(),
	}
}

// TestApplyHandoffToUserEmitsEvent verifies the happy path: a fresh
// task with no prior handoff_count produces exactly one
// agent.task.handoff_to_user event with the expected actor_agent_id
// and reason="stuck", plus a memo patch that flips handoff_status to
// handed_back and increments handoff_count.
func TestApplyHandoffToUserEmitsEvent(t *testing.T) {
	t.Parallel()
	q := newFakeHandoffQuerier()
	d := &fakeActorDisabler{}
	r := makeTaskRow(t)
	act := &Action{Kind: KindHandoffToUser, Confidence: 0.8, Reason: "agent stuck"}
	exec := &Executor{}

	emitted, err := exec.applyHandoffToUser(context.Background(), q, d, r, act, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("applyHandoffToUser: %v", err)
	}
	if !emitted {
		t.Fatal("expected emitted=true")
	}

	if got := len(q.handoffEvents); got != 1 {
		t.Fatalf("want 1 handoff event, got %d", got)
	}
	evt := q.handoffEvents[0]
	if !evt.ActorAgentID.Valid || evt.ActorAgentID.Int32 != int32(r.agentID) { //#nosec G115 -- ai_agents.id fits int32
		t.Fatalf("actor_agent_id = %+v, want valid=%d", evt.ActorAgentID, r.agentID)
	}
	if !evt.TaskID.Valid || evt.TaskID.Int32 != int32(r.id) { //#nosec G115 -- tasks.id fits int32
		t.Fatalf("task_id = %+v, want valid=%d", evt.TaskID, r.id)
	}
	if evt.WorkspaceID != r.workspaceID {
		t.Fatalf("workspace_id = %d, want %d", evt.WorkspaceID, r.workspaceID)
	}
	var payload map[string]any
	if err := json.Unmarshal(evt.PayloadJson, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got := payload["reason"]; got != "stuck" {
		t.Fatalf("reason = %v, want stuck", got)
	}
	if got := payload["detectedBy"]; got != "auto_action" {
		t.Fatalf("detectedBy = %v, want auto_action", got)
	}
	if got := payload["agentPublicId"]; got != r.agentPublicID.String() {
		t.Fatalf("agentPublicId = %v, want %s", got, r.agentPublicID.String())
	}
	if got := payload["taskPublicId"]; got != r.publicID.String() {
		t.Fatalf("taskPublicId = %v, want %s", got, r.publicID.String())
	}

	if got := len(d.calls); got != 1 {
		t.Fatalf("want 1 disable call, got %d", got)
	}
	if d.calls[0] != (disableCall{workspaceID: r.workspaceID, taskID: r.id, agentID: r.agentID}) {
		t.Fatalf("disable call = %+v", d.calls[0])
	}

	memo := q.snapshotMemo(r.id)
	if got := memo["handoff_status"]; got != "handed_back" {
		t.Fatalf("handoff_status = %v, want handed_back", got)
	}
	if got := memo["handoff_reason"]; got != "stuck" {
		t.Fatalf("handoff_reason = %v, want stuck", got)
	}
	if got := memo["handoff_count"]; got != float64(1) {
		t.Fatalf("handoff_count = %v, want 1", got)
	}
	if _, ok := memo["last_handoff_at"]; !ok {
		t.Fatalf("last_handoff_at missing: %+v", memo)
	}
}

// TestApplyHandoffToUserIncrementsExistingCount seeds the memo with a
// prior handoff_count=2 and asserts the executor increments to 3
// (well below the default loop limit of 5).
func TestApplyHandoffToUserIncrementsExistingCount(t *testing.T) {
	t.Parallel()
	q := newFakeHandoffQuerier()
	d := &fakeActorDisabler{}
	r := makeTaskRow(t)
	q.memoState[r.id] = map[string]any{
		"handoff_count": float64(2),
		"attempts":      float64(7),
	}

	exec := &Executor{}
	emitted, err := exec.applyHandoffToUser(context.Background(), q, d, r, &Action{Kind: KindHandoffToUser, Confidence: 0.8}, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("applyHandoffToUser: %v", err)
	}
	if !emitted {
		t.Fatal("expected emitted=true")
	}
	memo := q.snapshotMemo(r.id)
	if got := memo["handoff_count"]; got != float64(3) {
		t.Fatalf("handoff_count = %v, want 3", got)
	}
}

// TestApplyHandoffToUserLoopBudgetExhausted verifies that when prior
// handoff_count >= limit, the executor skips both the event and the
// actor disable, and does not mutate agent_memo.
func TestApplyHandoffToUserLoopBudgetExhausted(t *testing.T) {
	t.Parallel()
	q := newFakeHandoffQuerier()
	d := &fakeActorDisabler{}
	r := makeTaskRow(t)
	q.memoState[r.id] = map[string]any{
		"handoff_count": float64(2),
	}

	exec := &Executor{HandoffLoopLimit: 2}
	emitted, err := exec.applyHandoffToUser(context.Background(), q, d, r, &Action{Kind: KindHandoffToUser}, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("applyHandoffToUser: %v", err)
	}
	if emitted {
		t.Fatal("expected emitted=false on loop budget exhausted")
	}
	if got := len(q.handoffEvents); got != 0 {
		t.Fatalf("want 0 handoff events, got %d", got)
	}
	if got := len(d.calls); got != 0 {
		t.Fatalf("want 0 disable calls, got %d", got)
	}
	if got := len(q.memoUpdates); got != 0 {
		t.Fatalf("want 0 memo updates, got %d", got)
	}
	memo := q.snapshotMemo(r.id)
	if got := memo["handoff_count"]; got != float64(2) {
		t.Fatalf("handoff_count = %v, want untouched 2", got)
	}
}

// TestApplyHandoffToUserDefaultLoopLimit confirms the fallback constant
// kicks in when Executor.HandoffLoopLimit is zero. Five prior handoffs
// should be enough to trip the default cap.
func TestApplyHandoffToUserDefaultLoopLimit(t *testing.T) {
	t.Parallel()
	q := newFakeHandoffQuerier()
	d := &fakeActorDisabler{}
	r := makeTaskRow(t)
	q.memoState[r.id] = map[string]any{
		"handoff_count": float64(defaultHandoffLoopLimit),
	}
	exec := &Executor{} // HandoffLoopLimit=0 → fallback constant
	emitted, err := exec.applyHandoffToUser(context.Background(), q, d, r, &Action{Kind: KindHandoffToUser}, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("applyHandoffToUser: %v", err)
	}
	if emitted {
		t.Fatal("expected emitted=false at default cap")
	}
}

// TestReadMemoCountersDecodes covers the agent_memo decoding helper:
// missing memo → zeros, populated memo → the right fields.
func TestReadMemoCountersDecodes(t *testing.T) {
	t.Parallel()
	q := newFakeHandoffQuerier()
	attempts, count := readMemoCounters(context.Background(), q, 1, 1)
	if attempts != 0 || count != 0 {
		t.Fatalf("empty memo = (%d, %d), want (0, 0)", attempts, count)
	}
	q.memoState[1] = map[string]any{
		"attempts":      float64(4),
		"handoff_count": float64(2),
	}
	attempts, count = readMemoCounters(context.Background(), q, 7, 1)
	if attempts != 4 || count != 2 {
		t.Fatalf("populated memo = (%d, %d), want (4, 2)", attempts, count)
	}
}

// TestDecodeAgentMemo exercises the JSON decoder against the shape the
// orchestrator runtime writes into tasks.agent_memo.
func TestDecodeAgentMemo(t *testing.T) {
	t.Parallel()
	attempts, finished := decodeAgentMemo(nil)
	if attempts != 0 || !finished.IsZero() {
		t.Fatalf("nil memo = (%d, %v)", attempts, finished)
	}
	raw := []byte(`{"attempts": 3, "last_finished_at": 1700000000}`)
	attempts, finished = decodeAgentMemo(raw)
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if got := finished.Unix(); got != 1_700_000_000 {
		t.Fatalf("last_finished_at = %d, want 1700000000", got)
	}
}
