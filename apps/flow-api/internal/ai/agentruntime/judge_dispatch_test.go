package agentruntime

import (
	"context"
	"testing"
	"time"
)

// fakeJudgeExecutor records every call so the dispatch tests can
// assert exactly one judge invocation per judge-shaped run.
type fakeJudgeExecutor struct {
	calls     int
	gotWsID   uint32
	gotAgent  uint32
	gotSignal int64
	result    ExecutionResult
	err       error
}

func (f *fakeJudgeExecutor) ExecuteJudge(_ context.Context, wsID, agentID uint32, signalID int64) (ExecutionResult, error) {
	f.calls++
	f.gotWsID = wsID
	f.gotAgent = agentID
	f.gotSignal = signalID
	return f.result, f.err
}

// TestParseJudgeDedupeKey covers the parse logic the runner uses to
// decide between the task-agent and judge paths. Mirrors the cases in
// signaljudge/enqueuer_test.go so a shape regression breaks both.
func TestParseJudgeDedupeKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key      string
		wantOK   bool
		wantSnal int64
	}{
		{key: "judge:1:42", wantOK: true, wantSnal: 42},
		{key: "judge:4000000000:9000000000", wantOK: true, wantSnal: 9000000000},
		{key: "", wantOK: false},
		{key: "123:1700000000", wantOK: false},
		{key: "judge:1:abc", wantOK: false},
		{key: "judge:1:0", wantOK: false},
		{key: "judge:1", wantOK: false},
		{key: "judge:1:2:3", wantOK: false},
	}
	for _, tc := range cases {
		got, ok := parseJudgeDedupeKey(tc.key)
		if ok != tc.wantOK {
			t.Fatalf("parseJudgeDedupeKey(%q) ok=%v want %v", tc.key, ok, tc.wantOK)
		}
		if ok && got != tc.wantSnal {
			t.Fatalf("parseJudgeDedupeKey(%q) signal=%d want %d", tc.key, got, tc.wantSnal)
		}
	}
}

// TestRunnerDispatchesToJudge asserts a judge-shaped dedupe_key
// routes through JudgeExecutor and bypasses the task-agent
// AgentExecutor.
func TestRunnerDispatchesToJudge(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	judge := &fakeJudgeExecutor{result: ExecutionResult{LastThought: "ok"}}
	task := &fakeExecutor{}
	r := &OrchestratorRunner{
		Queries:  q,
		Executor: task,
		Judge:    judge,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}

	job := Job{AgentID: 7, WsID: 3, DedupeKey: "judge:7:42"}
	if err := r.Run(context.Background(), job, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("judge calls = %d, want 1", judge.calls)
	}
	if task.calls != 0 {
		t.Fatalf("task executor called %d times, want 0", task.calls)
	}
	if judge.gotSignal != 42 {
		t.Fatalf("judge signal = %d, want 42", judge.gotSignal)
	}
	if judge.gotWsID != 3 || judge.gotAgent != 7 {
		t.Fatalf("judge ws/agent = %d/%d, want 3/7", judge.gotWsID, judge.gotAgent)
	}
}

// TestRunnerFallsBackWhenJudgeMissing asserts a judge-shaped dedupe
// key with no JudgeExecutor configured still drives the task-agent
// path (so the queue does not stall) and logs a warning.
func TestRunnerFallsBackWhenJudgeMissing(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	task := &fakeExecutor{result: ExecutionResult{LastThought: "fallback"}}
	r := &OrchestratorRunner{
		Queries:  q,
		Executor: task,
		Judge:    nil, // intentionally absent
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}

	job := Job{AgentID: 7, WsID: 3, DedupeKey: "judge:7:42"}
	if err := r.Run(context.Background(), job, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if task.calls != 1 {
		t.Fatalf("task executor calls = %d, want 1 (fallback path)", task.calls)
	}
}

// TestRunnerTaskAgentPath asserts a non-judge dedupe_key takes the
// task-agent branch and never touches the JudgeExecutor.
func TestRunnerTaskAgentPath(t *testing.T) {
	t.Parallel()
	q := newFakeQuerier()
	judge := &fakeJudgeExecutor{}
	task := &fakeExecutor{result: ExecutionResult{LastThought: "ok"}}
	r := &OrchestratorRunner{
		Queries:  q,
		Executor: task,
		Judge:    judge,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	job := Job{AgentID: 1, WsID: 1, DedupeKey: "1:1700000000"}
	if err := r.Run(context.Background(), job, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if task.calls != 1 {
		t.Fatalf("task calls = %d, want 1", task.calls)
	}
	if judge.calls != 0 {
		t.Fatalf("judge calls = %d, want 0", judge.calls)
	}
}
