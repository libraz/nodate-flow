package agentruntime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
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
//
// The event writes are not retried here: the runner hands them to an
// auto-commit or transactional handle, and that handle owns the retry.
// What the runner owns is that a lost event never fails the run.
type flakyQuerier struct {
	*fakeRunnerQuerier

	fmu          sync.Mutex
	injected     error
	failEvents   bool
	failMemo     int
	memoAttempts int
}

func newFlakyQuerier(injected error) *flakyQuerier {
	return &flakyQuerier{
		fakeRunnerQuerier: newFakeQuerier(),
		injected:          injected,
	}
}

func (f *flakyQuerier) AppendRunEvent(ctx context.Context, evt eventbus.Event) error {
	f.fmu.Lock()
	fail := f.failEvents
	f.fmu.Unlock()
	if fail {
		return f.injected
	}
	return f.fakeRunnerQuerier.AppendRunEvent(ctx, evt)
}

func (f *flakyQuerier) RecordHandBack(ctx context.Context, ws uint32, h mutationlog.HandBack) error {
	f.fmu.Lock()
	fail := f.failEvents
	f.fmu.Unlock()
	if fail {
		return f.injected
	}
	return f.fakeRunnerQuerier.RecordHandBack(ctx, ws, h)
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

func (f *flakyQuerier) memoCount() int {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	return f.memoAttempts
}

// newRunnerWithQuerier builds a runner around a querier that also writes
// the run's events. DB stays nil, keeping the agent public id and source
// task resolution out of the way.
func newRunnerWithQuerier(q interface {
	RunnerQuerier
	RunEventWriter
}, exec AgentExecutor,
) *OrchestratorRunner {
	return &OrchestratorRunner{
		Queries:  q,
		Events:   q,
		Executor: exec,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
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
	if memos := q.memoCount(); memos != 2 {
		t.Fatalf("memo attempts = %d, want 2 (one failure + one success)", memos)
	}
}

// TestRunnerSurvivesLostEvents states what happens when an event write
// fails after its handle's retries: the event really is lost, and that is
// still not allowed to stop the agent loop or turn a successful run, or a
// hand-back, into a failed one.
func TestRunnerSurvivesLostEvents(t *testing.T) {
	t.Parallel()

	q := newFlakyQuerier(deadlockErr())
	q.failEvents = true
	r := newRunnerWithQuerier(q, &fakeExecutor{result: ExecutionResult{Confidence: 0.3}})

	if err := r.Run(context.Background(), Job{AgentID: 5, WsID: 6}, time.Time{}); err != nil {
		t.Fatalf("Run returned err: %v; a lost event must not wedge the loop", err)
	}
	if got := len(q.agentEvents) + len(q.handoffEvents); got != 0 {
		t.Fatalf("recorded %d events, want 0 with every write failing", got)
	}
}
