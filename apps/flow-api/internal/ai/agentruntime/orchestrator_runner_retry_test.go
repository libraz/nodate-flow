package agentruntime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// deadlockErr is what InnoDB returns when it picks this transaction as
// the victim of a lock cycle. The whole transaction is rolled back and
// the server's instruction to the client is to re-issue it, so nothing
// was written and a retry cannot duplicate a row.
func deadlockErr() error {
	return &mysql.MySQLError{
		Number:  1213,
		Message: "Deadlock found when trying to get lock; try restarting transaction",
	}
}

// duplicateKeyErr stands in for the errors no amount of retrying fixes.
func duplicateKeyErr() error {
	return &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
}

// flakyQuerier wraps the fake querier with deterministic fault injection:
// each counter is the number of leading calls to that method that fail
// with injected before the wrapped implementation is allowed to run.
// Attempt counters record how many times the runner actually called
// through, which is how a missing retry is told apart from a retry that
// happened to succeed.
type flakyQuerier struct {
	*fakeRunnerQuerier

	fmu             sync.Mutex
	injected        error
	failEventTypes  map[string]int
	failHandoff     int
	failMemo        int
	eventAttempts   int
	handoffAttempts int
	memoAttempts    int
}

func newFlakyQuerier(injected error) *flakyQuerier {
	return &flakyQuerier{
		fakeRunnerQuerier: newFakeQuerier(),
		injected:          injected,
		failEventTypes:    map[string]int{},
	}
}

func (f *flakyQuerier) AppendAgentEvent(ctx context.Context, arg generated.AppendAgentEventParams) (int64, error) {
	f.fmu.Lock()
	f.eventAttempts++
	if n := f.failEventTypes[arg.Type]; n > 0 {
		f.failEventTypes[arg.Type] = n - 1
		f.fmu.Unlock()
		return 0, f.injected
	}
	f.fmu.Unlock()
	return f.fakeRunnerQuerier.AppendAgentEvent(ctx, arg)
}

func (f *flakyQuerier) InsertHandoffToUserEvent(ctx context.Context, arg generated.InsertHandoffToUserEventParams) (int64, error) {
	f.fmu.Lock()
	f.handoffAttempts++
	if f.failHandoff > 0 {
		f.failHandoff--
		f.fmu.Unlock()
		return 0, f.injected
	}
	f.fmu.Unlock()
	return f.fakeRunnerQuerier.InsertHandoffToUserEvent(ctx, arg)
}

func (f *flakyQuerier) UpdateTaskAgentMemo(ctx context.Context, arg generated.UpdateTaskAgentMemoParams) (int64, error) {
	f.fmu.Lock()
	f.memoAttempts++
	if f.failMemo > 0 {
		f.failMemo--
		f.fmu.Unlock()
		return 0, f.injected
	}
	f.fmu.Unlock()
	return f.fakeRunnerQuerier.UpdateTaskAgentMemo(ctx, arg)
}

func (f *flakyQuerier) counts() (events, handoffs, memos int) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	return f.eventAttempts, f.handoffAttempts, f.memoAttempts
}

// newRunnerWithQuerier builds a runner around an arbitrary RunnerQuerier.
// DB stays nil: the run event path needs no *sql.DB, and leaving it out
// keeps the agent public id and source task resolution out of the way.
func newRunnerWithQuerier(q RunnerQuerier, exec AgentExecutor) *OrchestratorRunner {
	return &OrchestratorRunner{
		Queries:  q,
		Executor: exec,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

// TestRunnerRetriesTransientEventAppends is the case that was observed in
// the wild: under parallel load an event append lost a lock race, the
// error was swallowed, and the run's timeline kept a started with no
// completed while Run still reported success.
//
// Both appends fail once with a deadlock and then succeed, so a runner
// that retries ends the run with the full pair and a runner that does not
// ends it with nothing.
func TestRunnerRetriesTransientEventAppends(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(deadlockErr())
	q.failEventTypes["ai.agent.run.started"] = 1
	q.failEventTypes["ai.agent.run.completed"] = 1
	r := newRunnerWithQuerier(q, &fakeExecutor{result: ExecutionResult{LastThought: "ok"}})

	if err := r.Run(context.Background(), Job{AgentID: 42, WsID: 7}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}

	want := []string{"ai.agent.run.started", "ai.agent.run.completed"}
	if got := q.eventTypes(); !equalSlices(got, want) {
		t.Fatalf("event sequence = %v, want %v; a transient lock conflict must not cost the run an event", got, want)
	}
	if events, _, _ := q.counts(); events != 4 {
		t.Fatalf("append attempts = %d, want 4 (one failure + one success per event)", events)
	}
}

// TestRunnerRetriesTransientHandoffAppend covers the row that carries a
// stuck agent to a human. Losing it to a lock conflict means nobody is
// told, and the run still looks healthy from the outside.
func TestRunnerRetriesTransientHandoffAppend(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(deadlockErr())
	q.failHandoff = 1
	r := newRunnerWithQuerier(q, &fakeExecutor{result: ExecutionResult{Confidence: 0.3}})

	if err := r.Run(context.Background(), Job{AgentID: 1, WsID: 2}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}

	if got := len(q.handoffEvents); got != 1 {
		t.Fatalf("handoff events = %d, want 1", got)
	}
	if _, handoffs, _ := q.counts(); handoffs != 2 {
		t.Fatalf("handoff attempts = %d, want 2 (one failure + one success)", handoffs)
	}
}

// TestRunnerRetriesTransientMemoWrite covers tasks.agent_memo, where a
// dropped write silently rewinds the handoff loop budget and the attempts
// counter for the next run to read.
func TestRunnerRetriesTransientMemoWrite(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(deadlockErr())
	q.failMemo = 1
	r := newRunnerWithQuerier(q, nil)

	r.mergeMemo(context.Background(), 1, 11, map[string]any{"attempts": 3})

	if got := q.snapshotMemo(11)["attempts"]; got != float64(3) {
		t.Fatalf("attempts = %v, want 3; the memo patch was dropped on a retryable error", got)
	}
	if _, _, memos := q.counts(); memos != 2 {
		t.Fatalf("memo attempts = %d, want 2 (one failure + one success)", memos)
	}
}

// TestRunnerDoesNotRetryPermanentFailures pins the other half of the
// rule. Retrying is for the class MySQL asks the caller to re-issue;
// everything else must be attempted once and then swallowed, exactly as
// before, so the agent loop is neither wedged nor made to spend its
// budget re-sending a write that will never land.
func TestRunnerDoesNotRetryPermanentFailures(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(duplicateKeyErr())
	// Large enough that every attempt fails.
	q.failEventTypes["ai.agent.run.started"] = 99
	q.failEventTypes["ai.agent.run.completed"] = 99
	r := newRunnerWithQuerier(q, &fakeExecutor{result: ExecutionResult{LastThought: "ok"}})

	if err := r.Run(context.Background(), Job{AgentID: 3, WsID: 4}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v; a lost event must not fail the run", err)
	}
	if events, _, _ := q.counts(); events != 2 {
		t.Fatalf("append attempts = %d, want 2 (one per event, no retries)", events)
	}
}

// TestRunnerSurvivesExhaustedRetries states what happens at the end of
// the retry schedule: the run event really is lost, and that is still not
// allowed to stop the agent loop or turn a successful run into a failed
// one. The original best-effort intent is what bounds the fix.
func TestRunnerSurvivesExhaustedRetries(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(deadlockErr())
	q.failEventTypes["ai.agent.run.started"] = 99
	q.failEventTypes["ai.agent.run.completed"] = 99
	r := newRunnerWithQuerier(q, &fakeExecutor{result: ExecutionResult{LastThought: "ok"}})

	if err := r.Run(context.Background(), Job{AgentID: 5, WsID: 6}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v; exhausted event retries must not wedge the loop", err)
	}
	if got := len(q.agentEvents); got != 0 {
		t.Fatalf("recorded %d events, want 0 with every attempt failing", got)
	}
	if events, _, _ := q.counts(); events != 2*dbretry.MaxAttempts {
		t.Fatalf("append attempts = %d, want %d (the full schedule per event)", events, 2*dbretry.MaxAttempts)
	}
}

// TestRunnerRetriedAppendKeepsOneIdentity guards the retry itself: the
// row's public id is chosen before the first attempt, so a re-issued
// append is the same row rather than a second one that a unique index
// would not catch.
func TestRunnerRetriedAppendKeepsOneIdentity(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(deadlockErr())
	q.failEventTypes["ai.agent.run.started"] = 1
	r := newRunnerWithQuerier(q, &fakeExecutor{result: ExecutionResult{LastThought: "ok"}})

	if err := r.Run(context.Background(), Job{AgentID: 8, WsID: 9}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range q.agentEvents {
		id := e.PublicID.String()
		if seen[id] {
			t.Fatalf("public id %s was written twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 2 {
		t.Fatalf("distinct events = %d, want 2", len(seen))
	}
}
