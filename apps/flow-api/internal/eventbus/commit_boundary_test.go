package eventbus

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
)

// The stub driver below accepts every statement and reports one
// affected row. It exists so these tests can hold a real *sql.Tx — the
// type the fan-out decision branches on — without a server. Nothing
// inspects the SQL; the append path only needs the INSERT to succeed
// and yield a last insert id.
type (
	stubDriver struct{}
	stubConn   struct{}
	stubStmt   struct{}
	stubTx     struct{}
	stubRows   struct{}
	stubResult struct{}
)

func (stubDriver) Open(string) (driver.Conn, error) { return stubConn{}, nil }

func (stubConn) Prepare(string) (driver.Stmt, error) { return stubStmt{}, nil }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return stubTx{}, nil }

// ExecContext keeps database/sql off the Prepare path, so the stub does
// not have to model placeholders.
func (stubConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return stubResult{}, nil
}

// CheckNamedValue accepts the driver.Valuer types the generated params
// carry (types.PublicID, sql.NullInt32, json.RawMessage) without
// conversion.
func (stubConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (stubStmt) Close() error                               { return nil }
func (stubStmt) NumInput() int                              { return -1 }
func (stubStmt) Exec([]driver.Value) (driver.Result, error) { return stubResult{}, nil }
func (stubStmt) Query([]driver.Value) (driver.Rows, error)  { return stubRows{}, nil }

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

func (stubRows) Columns() []string              { return nil }
func (stubRows) Close() error                   { return nil }
func (stubRows) Next([]driver.Value) error      { return io.EOF }
func (stubResult) LastInsertId() (int64, error) { return 4242, nil }
func (stubResult) RowsAffected() (int64, error) { return 1, nil }

var registerStubOnce sync.Once

// stubDB registers the stub driver once per process and returns a pool
// backed by it.
func stubDB(t *testing.T) *sql.DB {
	t.Helper()
	registerStubOnce.Do(func() { sql.Register("eventbus-stub", stubDriver{}) })
	db, err := sql.Open("eventbus-stub", "")
	if err != nil {
		t.Fatalf("open stub db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fanOutCounter is a subscriber that just counts dispatches.
type fanOutCounter struct {
	mu    sync.Mutex
	count int
}

func (c *fanOutCounter) hook() NotifyHook {
	return func(context.Context, uint32, string, uint64) {
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

// subscribe registers c for the duration of the test.
func (c *fanOutCounter) subscribe(t *testing.T) {
	t.Helper()
	handle := AddNotifyHook(c.hook())
	t.Cleanup(func() { RemoveNotifyHook(handle) })
}

// TestFanOutRefusedInHandRolledTx pins the enforcement: an append
// inside a transaction the caller opened by hand has no commit boundary
// to defer the fan-out to, so subscribers must not be woken at all.
// Waking them there hands every one of them an event that is not yet
// readable on their own connection, which they can only resolve to
// nothing — a delivery that quietly evaporates. Silence plus one loud
// log line naming the caller is the honest outcome.
func TestFanOutRefusedInHandRolledTx(t *testing.T) {
	db := stubDB(t)
	var counter fanOutCounter
	counter.subscribe(t)

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })

	if err := Append(ctx, tx, Event{Type: TaskCreated, WorkspaceID: 7}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := counter.fired(); got != 0 {
		t.Fatalf("fan-out must be refused without a commit boundary, fired %d times", got)
	}
}

// TestFanOutDeferredUntilCommit is the other half: through
// dbretry.InTx the subscribers do run, and only once the transaction
// has committed.
func TestFanOutDeferredUntilCommit(t *testing.T) {
	db := stubDB(t)
	var counter fanOutCounter
	counter.subscribe(t)

	firedInside := false
	err := dbretry.InTx(context.Background(), db, "eventbus.test", nil, func(ctx context.Context, tx *sql.Tx) error {
		if err := Append(ctx, tx, Event{Type: TaskCreated, WorkspaceID: 7}); err != nil {
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
	counter.subscribe(t)

	if err := Append(context.Background(), db, Event{Type: TaskCreated, WorkspaceID: 7}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if got := counter.fired(); got != 1 {
		t.Fatalf("auto-commit append must fan out immediately, fired %d times", got)
	}
}
