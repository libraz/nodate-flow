package eventbus

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
)

// The stub driver below accepts every statement and reports one
// affected row. It exists so these tests can hold a real transaction
// without a server. Nothing inspects the SQL; the append path only needs
// the INSERT to succeed and yield a last insert id.
//
// A pool can also be told to fail every statement, which is how the
// dropped-append tests get a driver error out of a real handle instead
// of a hand-written executor: the appenders take a commit boundary, and
// only this package can produce one.
type (
	stubDriver struct{}
	stubConn   struct{ fail *stubFailure }
	stubStmt   struct{}
	stubTx     struct{ fail *stubFailure }
	stubRows   struct{}
	stubResult struct{}
)

// stubFailure is the error a named pool returns for every statement,
// plus how many statements reached it and how its transactions ended. It
// is keyed by DSN rather than held in a package-level variable so
// parallel tests cannot see each other's failures.
//
// Recording commits and rollbacks separately from the returned error is
// what lets a test ask the question that matters for a lost append: not
// "what did the caller get back" but "did the transaction land".
type stubFailure struct {
	mu sync.Mutex
	// err is returned for every statement past the first skip.
	err error
	// skip is how many statements succeed before err starts. It exists so
	// a test can let the work a transaction did land and fail only the
	// append that was supposed to accompany it.
	skip      int
	calls     int
	commits   int
	rollbacks int
}

func (f *stubFailure) commit() {
	f.mu.Lock()
	f.commits++
	f.mu.Unlock()
}

func (f *stubFailure) rollback() {
	f.mu.Lock()
	f.rollbacks++
	f.mu.Unlock()
}

// outcomes returns how many transactions on this pool committed and how
// many rolled back.
func (f *stubFailure) outcomes() (commits, rollbacks int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commits, f.rollbacks
}

func (f *stubFailure) record() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.skip {
		return nil
	}
	return f.err
}

func (f *stubFailure) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var (
	stubFailuresMu sync.Mutex
	stubFailures   = map[string]*stubFailure{}
)

func (stubDriver) Open(name string) (driver.Conn, error) {
	stubFailuresMu.Lock()
	defer stubFailuresMu.Unlock()
	return stubConn{fail: stubFailures[name]}, nil
}

func (c stubConn) Prepare(string) (driver.Stmt, error) { return stubStmt{}, nil }
func (c stubConn) Close() error                        { return nil }
func (c stubConn) Begin() (driver.Tx, error)           { return stubTx(c), nil }

// ExecContext keeps database/sql off the Prepare path, so the stub does
// not have to model placeholders.
func (c stubConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if c.fail != nil {
		if err := c.fail.record(); err != nil {
			return nil, err
		}
	}
	return stubResult{}, nil
}

// CheckNamedValue accepts the driver.Valuer types the generated params
// carry (types.PublicID, sql.NullInt32, json.RawMessage) without
// conversion.
func (c stubConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (stubStmt) Close() error                               { return nil }
func (stubStmt) NumInput() int                              { return -1 }
func (stubStmt) Exec([]driver.Value) (driver.Result, error) { return stubResult{}, nil }
func (stubStmt) Query([]driver.Value) (driver.Rows, error)  { return stubRows{}, nil }

func (t stubTx) Commit() error {
	if t.fail != nil {
		t.fail.commit()
	}
	return nil
}

func (t stubTx) Rollback() error {
	if t.fail != nil {
		t.fail.rollback()
	}
	return nil
}

func (stubRows) Columns() []string              { return nil }
func (stubRows) Close() error                   { return nil }
func (stubRows) Next([]driver.Value) error      { return io.EOF }
func (stubResult) LastInsertId() (int64, error) { return 4242, nil }
func (stubResult) RowsAffected() (int64, error) { return 1, nil }

var (
	registerStubOnce sync.Once
	stubDSNSeq       atomic.Int64
)

// openStub registers the stub driver once per process and returns a pool
// backed by it under the given DSN.
func openStub(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	registerStubOnce.Do(func() { sql.Register("eventbus-stub", stubDriver{}) })
	db, err := sql.Open("eventbus-stub", dsn)
	if err != nil {
		t.Fatalf("open stub db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// stubDB returns a pool whose statements all succeed.
func stubDB(t *testing.T) *sql.DB {
	t.Helper()
	return openStub(t, "")
}

// recordingStubDB returns a pool whose statements all succeed, together
// with the record of what its transactions did. It is failingStubDB with
// no failure: the recorder is the point.
func recordingStubDB(t *testing.T) (*sql.DB, *stubFailure) {
	t.Helper()
	return failingStubDB(t, nil)
}

// failingStubDB returns a pool whose every statement fails with err,
// together with the counter of statements that reached the driver and
// the record of what its transactions did.
func failingStubDB(t *testing.T, err error) (*sql.DB, *stubFailure) {
	t.Helper()
	return failingStubDBAfter(t, 0, err)
}

// failingStubDBAfter returns a pool whose first skip statements succeed
// and whose every statement after that fails with err.
func failingStubDBAfter(t *testing.T, skip int, err error) (*sql.DB, *stubFailure) {
	t.Helper()
	dsn := "fail-" + strconv.FormatInt(stubDSNSeq.Add(1), 10)
	f := &stubFailure{err: err, skip: skip}
	stubFailuresMu.Lock()
	stubFailures[dsn] = f
	stubFailuresMu.Unlock()
	t.Cleanup(func() {
		stubFailuresMu.Lock()
		delete(stubFailures, dsn)
		stubFailuresMu.Unlock()
	})
	return openStub(t, dsn), f
}

// errStubInsert is what the failing pool returns for the event INSERT.
var errStubInsert = errors.New("stub: insert refused")

// fanOutCounter is a subscriber that counts the dispatches belonging to
// one workspace.
//
// The workspace filter is what makes the count mean anything. Notify
// hooks are registered process-wide, so a hook added by one test is
// called for every append any concurrently running test makes; a counter
// that took all of them would report its neighbours' successful
// dispatches, and the tests that assert zero would fail depending on
// which ones happened to overlap. Each test therefore appends under its
// own workspace and counts only that one.
type fanOutCounter struct {
	workspaceID uint32
	mu          sync.Mutex
	count       int
}

func (c *fanOutCounter) hook() NotifyHook {
	return func(_ context.Context, workspaceInternalID uint32, _ string, _ uint64) {
		if workspaceInternalID != c.workspaceID {
			return
		}
		c.mu.Lock()
		c.count++
		c.mu.Unlock()
	}
}

func (c *fanOutCounter) fired() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// subscribe registers c for the duration of the test, counting only
// dispatches for workspaceID. Appends made by the test must use the same
// workspace, or the counter observes nothing and every assertion on it
// passes vacuously.
func (c *fanOutCounter) subscribe(t *testing.T, workspaceID uint32) {
	t.Helper()
	c.workspaceID = workspaceID
	handle := AddNotifyHook(c.hook())
	t.Cleanup(func() { RemoveNotifyHook(handle) })
}

// An append inside a transaction the caller opened by hand has no
// commit boundary to defer the fan-out to, so waking subscribers there
// would hand every one of them an event that is not yet readable on
// their own connection — a delivery that quietly evaporates. There is no
// test for that case because Append takes a dbretry.CommitBoundary,
// which a bare *sql.Tx does not satisfy: the call does not compile.

// TestFanOutDeferredUntilCommit is the transaction half: through
// dbretry.InTx the subscribers do run, and only once the transaction
// has committed.
func TestFanOutDeferredUntilCommit(t *testing.T) {
	db := stubDB(t)
	var counter fanOutCounter
	counter.subscribe(t, 71)

	firedInside := false
	err := dbretry.InTx(context.Background(), db, "eventbus.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if err := Append(ctx, tx, Event{Type: TaskCreated, WorkspaceID: 71}); err != nil {
			return err
		}
		firedInside = counter.fired() > 0
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}

	if firedInside {
		t.Fatal("fan-out ran while the transaction was still open")
	}
	if got := counter.fired(); got != 1 {
		t.Fatalf("fan-out must run once after commit, ran %d times", got)
	}
}

// TestFanOutImmediateOnAutoCommit keeps the plain *sql.DB path intact:
// there is no transaction to wait for, so subscribers run right away.
func TestFanOutImmediateOnAutoCommit(t *testing.T) {
	db := stubDB(t)
	var counter fanOutCounter
	counter.subscribe(t, 72)

	if err := Append(context.Background(), dbretry.AutoCommit(db), Event{Type: TaskCreated, WorkspaceID: 72}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if got := counter.fired(); got != 1 {
		t.Fatalf("auto-commit append must fan out immediately, fired %d times", got)
	}
}

// TestAppendFailureRefusesTheCommitEvenWhenDiscarded is the point of
// routing an append failure back to the commit boundary.
//
// The closure here does what the swallow always looked like: it takes the
// error, ignores it, and reports success. The transaction still must not
// commit, because task state is derived from the event log (CLAUDE.md
// rule 8) and a mutation that lands without the row describing it is a
// wrong state nothing later corrects. Whether that holds is not up to
// the call site, which is why the call site is written the wrong way
// round on purpose.
func TestAppendFailureRefusesTheCommitEvenWhenDiscarded(t *testing.T) {
	t.Parallel()

	db, rec := failingStubDB(t, errStubInsert)

	err := dbretry.InTx(context.Background(), db, "eventbus.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		_ = Append(ctx, tx, Event{Type: TaskCreated, WorkspaceID: 7})
		return nil
	})
	if !errors.Is(err, errStubInsert) {
		t.Fatalf("InTx err = %v, want the append failure %v", err, errStubInsert)
	}
	commits, rollbacks := rec.outcomes()
	if commits != 0 {
		t.Fatalf("the transaction committed %d times; a lost event must not become durable", commits)
	}
	if rollbacks == 0 {
		t.Fatal("the transaction neither committed nor rolled back")
	}
}

// TestBestEffortAppendFailureKeepsTheTransaction pins the other half:
// [AppendBestEffort] is the sanctioned way to accept a dropped event, so
// it must stay usable inside a transaction. Poisoning the boundary there
// too would leave no way to express the choice at all.
func TestBestEffortAppendFailureKeepsTheTransaction(t *testing.T) {
	t.Parallel()

	db, rec := failingStubDB(t, errStubInsert)

	err := dbretry.InTx(context.Background(), db, "eventbus.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		AppendBestEffort(ctx, tx, Event{Type: TaskCreated, WorkspaceID: 7}, "eventbus.test")
		return nil
	})
	if err != nil {
		t.Fatalf("InTx err = %v, want nil: a best-effort append must not fail the transaction", err)
	}
	if commits, _ := rec.outcomes(); commits != 1 {
		t.Fatalf("the transaction committed %d times, want 1", commits)
	}
}

// TestAppendSuccessStillCommits guards the mechanism from the other side.
// A boundary that refused every commit would pass both tests above and be
// useless, so the untouched path is pinned alongside them.
func TestAppendSuccessStillCommits(t *testing.T) {
	t.Parallel()

	db, rec := recordingStubDB(t)

	err := dbretry.InTx(context.Background(), db, "eventbus.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return Append(ctx, tx, Event{Type: TaskCreated, WorkspaceID: 7})
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if commits, _ := rec.outcomes(); commits != 1 {
		t.Fatalf("the transaction committed %d times, want 1", commits)
	}
}

// TestFailedAppendRollsBackTheWorkItAccompanied is the shape a handler
// has once its event is appended inside the transaction that made the
// change: some writes, then the append describing them.
//
// The write here succeeds and only the append fails, which is the case
// the boundary exists for — the alternative is a committed change that
// nothing was told about. The closure discards the append error and
// reports success, and the transaction still does not commit.
//
// The fan-out assertion is the other half. Moving an append inside a
// transaction is how subscribers start being woken for rows that are not
// visible on their connection yet, so an attempt that rolls back must
// leave them silent.
func TestFailedAppendRollsBackTheWorkItAccompanied(t *testing.T) {
	t.Parallel()

	// One statement lands: the write the event is supposed to describe.
	db, rec := failingStubDBAfter(t, 1, errStubInsert)
	var counter fanOutCounter
	counter.subscribe(t, 73)

	err := dbretry.InTx(context.Background(), db, "eventbus.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE relation_suggestions SET status = 'accepted' WHERE id = ?", 1); e != nil {
			return e
		}
		_ = Append(ctx, tx, Event{Type: RelationAccepted, WorkspaceID: 73})
		return nil
	})
	if !errors.Is(err, errStubInsert) {
		t.Fatalf("InTx err = %v, want the append failure %v", err, errStubInsert)
	}
	commits, rollbacks := rec.outcomes()
	if commits != 0 {
		t.Fatalf("the transaction committed %d times; the write must not outlive its event", commits)
	}
	if rollbacks != 1 {
		t.Fatalf("the transaction rolled back %d times, want 1", rollbacks)
	}
	if fired := counter.fired(); fired != 0 {
		t.Fatalf("fan-out ran %d times for a transaction that rolled back", fired)
	}
}
