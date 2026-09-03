package dbretry

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
)

// The stub driver here exists so InTx can be exercised against a real
// *sql.Tx without a server. Nothing inspects the SQL; the tests only ask
// how the transaction ended.
type (
	failStubDriver struct{}
	failStubConn   struct{ rec *txRecord }
	failStubStmt   struct{}
	failStubTx     struct{ rec *txRecord }
	failStubRows   struct{}
	failStubResult struct{}
)

// txRecord counts how the transactions on one pool ended. It is keyed by
// DSN rather than held in a package-level variable so parallel tests
// cannot see each other's transactions.
type txRecord struct {
	mu        sync.Mutex
	commits   int
	rollbacks int
}

func (r *txRecord) outcomes() (commits, rollbacks int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits, r.rollbacks
}

var (
	txRecordsMu sync.Mutex
	txRecords   = map[string]*txRecord{}

	registerFailStubOnce sync.Once
	failStubDSNSeq       atomic.Int64
)

func (failStubDriver) Open(name string) (driver.Conn, error) {
	txRecordsMu.Lock()
	defer txRecordsMu.Unlock()
	return failStubConn{rec: txRecords[name]}, nil
}

func (c failStubConn) Prepare(string) (driver.Stmt, error) { return failStubStmt{}, nil }
func (c failStubConn) Close() error                        { return nil }
func (c failStubConn) Begin() (driver.Tx, error)           { return failStubTx{rec: c.rec}, nil }

func (t failStubTx) Commit() error {
	t.rec.mu.Lock()
	t.rec.commits++
	t.rec.mu.Unlock()
	return nil
}

func (t failStubTx) Rollback() error {
	t.rec.mu.Lock()
	t.rec.rollbacks++
	t.rec.mu.Unlock()
	return nil
}

func (failStubStmt) Close() error                               { return nil }
func (failStubStmt) NumInput() int                              { return -1 }
func (failStubStmt) Exec([]driver.Value) (driver.Result, error) { return failStubResult{}, nil }
func (failStubStmt) Query([]driver.Value) (driver.Rows, error)  { return failStubRows{}, nil }

func (failStubRows) Columns() []string              { return nil }
func (failStubRows) Close() error                   { return nil }
func (failStubRows) Next([]driver.Value) error      { return io.EOF }
func (failStubResult) LastInsertId() (int64, error) { return 1, nil }
func (failStubResult) RowsAffected() (int64, error) { return 1, nil }

// recordingDB returns a pool backed by the stub driver together with the
// record of how its transactions ended.
func recordingDB(t *testing.T) (*sql.DB, *txRecord) {
	t.Helper()
	registerFailStubOnce.Do(func() { sql.Register("dbretry-fail-stub", failStubDriver{}) })
	dsn := "rec-" + strconv.FormatInt(failStubDSNSeq.Add(1), 10)
	rec := &txRecord{}
	txRecordsMu.Lock()
	txRecords[dsn] = rec
	txRecordsMu.Unlock()
	t.Cleanup(func() {
		txRecordsMu.Lock()
		delete(txRecords, dsn)
		txRecordsMu.Unlock()
	})
	db, err := sql.Open("dbretry-fail-stub", dsn)
	if err != nil {
		t.Fatalf("open stub db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, rec
}

var errLostWrite = errors.New("dbretry: write lost")

// TestInTxRefusesCommitAfterFail is what makes [Tx.Fail] structural
// rather than advisory: the closure reports success, and the transaction
// still does not commit.
//
// Without this the caller of whatever reported the loss could log it and
// return nil, and the rest of the unit would become durable without it.
// That is the shape the mechanism exists to remove, so it is the shape
// the test uses.
func TestInTxRefusesCommitAfterFail(t *testing.T) {
	t.Parallel()

	db, rec := recordingDB(t)
	err := InTx(context.Background(), db, "test", nil, func(_ context.Context, tx *Tx) error {
		tx.Fail(errLostWrite)
		return nil
	})
	if !errors.Is(err, errLostWrite) {
		t.Fatalf("InTx err = %v, want %v", err, errLostWrite)
	}
	commits, rollbacks := rec.outcomes()
	if commits != 0 {
		t.Fatalf("committed %d times after a reported loss, want 0", commits)
	}
	if rollbacks != 1 {
		t.Fatalf("rolled back %d times, want 1", rollbacks)
	}
}

// TestInTxKeepsTheFirstFailure pins which error survives: the one that
// says why the transaction was abandoned, not whatever failed after it.
func TestInTxKeepsTheFirstFailure(t *testing.T) {
	t.Parallel()

	db, _ := recordingDB(t)
	later := errors.New("dbretry: consequence")
	err := InTx(context.Background(), db, "test", nil, func(_ context.Context, tx *Tx) error {
		tx.Fail(errLostWrite)
		tx.Fail(later)
		return nil
	})
	if !errors.Is(err, errLostWrite) {
		t.Fatalf("InTx err = %v, want the first failure %v", err, errLostWrite)
	}
}

// TestInTxCommitsWithoutFail keeps the mechanism honest from the other
// side. A boundary that refused every commit would satisfy the tests
// above and be useless.
func TestInTxCommitsWithoutFail(t *testing.T) {
	t.Parallel()

	db, rec := recordingDB(t)
	if err := InTx(context.Background(), db, "test", nil, func(_ context.Context, tx *Tx) error {
		tx.Fail(nil)
		return nil
	}); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if commits, _ := rec.outcomes(); commits != 1 {
		t.Fatalf("committed %d times, want 1", commits)
	}
}

// TestAutoCommitFailIsANoOp records the ceiling honestly: there is no
// boundary on the auto-commit path, so nothing can be withheld there and
// the static guard on the append entry points is what stands in for it.
func TestAutoCommitFailIsANoOp(t *testing.T) {
	t.Parallel()

	AutoCommit(nil).Fail(errLostWrite)
}
