package autoactions

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The memo write that closes a handoff carries `enabled = TRUE`, so it can
// succeed and match no row: the task was disabled or removed between the
// scan that chose it and the write. The two writes before it — the
// agent.task.handoff_to_user event and the disabled assignee row — then
// describe a hand-back whose handoff_count never moved, so the next pass
// reads the same loop budget, picks the same task, and hands it back
// again, reporting an applied action every time.
//
// All three writes run on one transaction, which is what makes refusing
// the pass a real remedy rather than a louder log line: the error takes
// the event and the assignee row back out with it. The stub-driven pair
// below is where that is visible — same task, same action, same
// statements, differing only in what the driver reports as the memo
// write's affected-row count.

// TestHandoffAppliedWhenTheMemoWriteLands is the positive control for the
// fake-driven half. The absence asserted in the case below means nothing
// unless these inputs are shown to produce a handoff.
func TestHandoffAppliedWhenTheMemoWriteLands(t *testing.T) {
	t.Parallel()

	q := newFakeHandoffQuerier()
	d := &fakeActorDisabler{}
	r := makeTaskRow(t)
	exec := &Executor{}

	emitted, err := exec.applyHandoffToUser(context.Background(), q, d, r, &Action{Kind: KindHandoffToUser}, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("applyHandoffToUser: %v", err)
	}
	if !emitted {
		t.Fatal("a handoff whose memo write landed must be reported as applied")
	}
	if got := q.snapshotMemo(r.id)["handoff_count"]; got != float64(1) {
		t.Errorf("handoff_count = %v, want 1; the counter that caps the loop did not advance", got)
	}
}

// TestHandoffRefusedWhenTheMemoWriteMatchesNoTask is the case the count
// answers: the pass must not report a handoff it did not complete, and
// must fail so the caller's transaction takes the event and the disabled
// assignee back out.
func TestHandoffRefusedWhenTheMemoWriteMatchesNoTask(t *testing.T) {
	t.Parallel()

	q := newFakeHandoffQuerier()
	d := &fakeActorDisabler{}
	r := makeTaskRow(t)
	q.memoWriteMisses(r.id)
	exec := &Executor{}

	emitted, err := exec.applyHandoffToUser(context.Background(), q, d, r, &Action{Kind: KindHandoffToUser}, time.Unix(1_700_000_000, 0).UTC())
	if err == nil {
		t.Fatal("a memo write that matched no row must fail the pass; the event and the " +
			"disabled assignee describe a hand-back the task never recorded")
	}
	if emitted {
		t.Error("a pass whose memo write matched no row reported a handoff as applied")
	}
	if got, ok := q.snapshotMemo(r.id)["handoff_count"]; ok {
		t.Errorf("handoff_count = %v; a write that matched no row must leave the memo as it was", got)
	}
}

// TestHandoffCommitsTheWritesItCompleted is the positive control for the
// stub-driven half: the three writes reach the driver and the transaction
// commits, which is what the refusal below is measured against.
func TestHandoffCommitsTheWritesItCompleted(t *testing.T) {
	t.Parallel()

	e, stub, logs := newHandoffExecutor(t, 1)
	e.handoffToUser(context.Background(), makeTaskRow(t), &Action{Kind: KindHandoffToUser})

	for _, stmt := range []string{"INSERT INTO events", "UPDATE task_actors", "SET agent_memo"} {
		if got := stub.issued(stmt); got != 1 {
			t.Errorf("%q ran %d times, want 1", stmt, got)
		}
	}
	if !logs.logged("auto-action applied: handoff_to_user") {
		t.Errorf("a handoff that happened must be reported as applied; logged %v", logs.seen())
	}
	commits, rollbacks := stub.outcomes()
	if commits != 1 || rollbacks != 0 {
		t.Errorf("the transaction committed %d times and rolled back %d; a completed handoff commits once",
			commits, rollbacks)
	}
}

// TestHandoffRollsBackWhenTheMemoWriteMatchesNoTask is the pair to it.
// The event insert and the assignee disable still reach the driver — the
// pass has no way to know the task is gone until the guarded write tells
// it — so what has to differ is the end of the transaction.
func TestHandoffRollsBackWhenTheMemoWriteMatchesNoTask(t *testing.T) {
	t.Parallel()

	e, stub, logs := newHandoffExecutor(t, 0)
	e.handoffToUser(context.Background(), makeTaskRow(t), &Action{Kind: KindHandoffToUser})

	if got := stub.issued("SET agent_memo"); got != 1 {
		t.Fatalf("the memo write ran %d times, want 1; the pass must still attempt it", got)
	}
	commits, rollbacks := stub.outcomes()
	if commits != 0 {
		t.Errorf("the transaction committed %d times; committing here keeps a handoff event and a "+
			"detached assignee for a hand-back the task never recorded", commits)
	}
	if rollbacks != 1 {
		t.Errorf("the transaction rolled back %d times, want 1", rollbacks)
	}
	if logs.logged("auto-action applied: handoff_to_user") {
		t.Errorf("a pass that handed nothing back reported an applied handoff; logged %v", logs.seen())
	}
	if logs.logged("auto-action skipped: handoff_to_user (loop budget exhausted)") {
		t.Errorf("a task that is gone was reported as a budget skip, which is a state the loop "+
			"can recover from; logged %v", logs.seen())
	}
	if !logs.logged("auto-action executor: handoff_to_user") {
		t.Errorf("an abandoned handoff must say so; logged %v", logs.seen())
	}
}

// --- stub driver ------------------------------------------------------

// handoffStub is the state one stub-backed pool records: the statements
// that reached it, how many rows the memo write reports, and how its
// transactions ended.
type handoffStub struct {
	mu sync.Mutex
	// memoRows is what the agent_memo UPDATE reports as affected. Zero is
	// the task having been disabled or removed before this pass reached
	// the write.
	memoRows   int64
	statements []string
	commits    int
	rollbacks  int
}

// answer records a statement and reports the result the stub gives it.
func (s *handoffStub) answer(query string) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, query)
	switch {
	case strings.Contains(query, "SET agent_memo"):
		return handoffResult{rows: s.memoRows}, nil
	case strings.Contains(query, "UPDATE task_actors"):
		// The assignee row the scan saw is still there, so the disable
		// lands and the memo write is what decides the pass.
		return handoffResult{rows: 1}, nil
	default:
		// The event INSERT, which needs a last insert id.
		return handoffResult{rows: 1, lastID: int64(len(s.statements))}, nil
	}
}

// issued counts the statements containing needle.
func (s *handoffStub) issued(needle string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, stmt := range s.statements {
		if strings.Contains(stmt, needle) {
			n++
		}
	}
	return n
}

// outcomes returns how many transactions committed and how many rolled
// back, so a refused pass can be told apart from a completed one.
func (s *handoffStub) outcomes() (commits, rollbacks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits, s.rollbacks
}

type handoffResult struct {
	rows   int64
	lastID int64
}

func (r handoffResult) LastInsertId() (int64, error) { return r.lastID, nil }
func (r handoffResult) RowsAffected() (int64, error) { return r.rows, nil }

type (
	handoffDriver struct{}
	handoffConn   struct{ stub *handoffStub }
	handoffTx     struct{ stub *handoffStub }
)

var (
	handoffStubsMu sync.Mutex
	handoffStubs   = map[string]*handoffStub{}
)

func (handoffDriver) Open(dsn string) (driver.Conn, error) {
	handoffStubsMu.Lock()
	defer handoffStubsMu.Unlock()
	stub, ok := handoffStubs[dsn]
	if !ok {
		return nil, fmt.Errorf("autoactions: no stub registered for dsn %q", dsn)
	}
	return handoffConn{stub: stub}, nil
}

// Prepare is never reached: the connection answers statements directly,
// so database/sql has no reason to fall back to a prepared statement. It
// fails loudly rather than answering, because a statement that took the
// prepare path would not be recorded and every assertion above would then
// hold vacuously.
func (c handoffConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("autoactions: the stub answers statements directly")
}

func (c handoffConn) Close() error { return nil }

func (c handoffConn) Begin() (driver.Tx, error) { return handoffTx(c), nil }

func (c handoffConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	return c.stub.answer(query)
}

// QueryContext answers the memo read the pass makes before it decides.
// The memo carries no prior handoff, so the loop budget is open and the
// pass goes on to the writes, which is where the count under test is.
func (c handoffConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.stub.mu.Lock()
	c.stub.statements = append(c.stub.statements, query)
	c.stub.mu.Unlock()
	return &handoffRows{memo: []byte(`{"attempts":2}`)}, nil
}

// CheckNamedValue accepts the argument types the handoff path binds
// (uint32 ids, types.PublicID, sql.NullInt32, json.RawMessage) without
// conversion. The stub reads the SQL, not the arguments.
func (c handoffConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (t handoffTx) Commit() error {
	t.stub.mu.Lock()
	defer t.stub.mu.Unlock()
	t.stub.commits++
	return nil
}

func (t handoffTx) Rollback() error {
	t.stub.mu.Lock()
	defer t.stub.mu.Unlock()
	t.stub.rollbacks++
	return nil
}

// handoffRows is the single-column answer to the agent_memo read.
type handoffRows struct {
	memo []byte
	done bool
}

func (r *handoffRows) Columns() []string { return []string{"agent_memo"} }
func (r *handoffRows) Close() error      { return nil }

func (r *handoffRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.memo
	return nil
}

var (
	registerHandoffDriverOnce sync.Once
	handoffDSNSeq             atomic.Int64
)

// newHandoffExecutor builds an executor over a stub pool whose agent_memo
// UPDATE reports memoRows.
func newHandoffExecutor(t *testing.T, memoRows int64) (*Executor, *handoffStub, *messageLog) {
	t.Helper()
	registerHandoffDriverOnce.Do(func() { sql.Register("autoactions-handoff-stub", handoffDriver{}) })

	// database/sql keeps a process-wide driver registry, so the per-test
	// state hangs off the DSN and each test takes its own.
	dsn := "handoff-" + strconv.FormatInt(handoffDSNSeq.Add(1), 10)
	stub := &handoffStub{memoRows: memoRows}
	handoffStubsMu.Lock()
	handoffStubs[dsn] = stub
	handoffStubsMu.Unlock()
	t.Cleanup(func() {
		handoffStubsMu.Lock()
		delete(handoffStubs, dsn)
		handoffStubsMu.Unlock()
	})

	db, err := sql.Open("autoactions-handoff-stub", dsn)
	if err != nil {
		t.Fatalf("open stub db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	logs := &messageLog{}
	return &Executor{DB: db, Logger: slog.New(logs)}, stub, logs
}
