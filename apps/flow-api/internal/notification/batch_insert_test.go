package notification

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// countingDriver records every statement executed against it so a test
// can count round trips. A real server is not needed: what matters is
// how many statements the fan-out sends, not what they return.
type countingDriver struct{}

type countingConn struct{}

type countingStmt struct{}

type countingTx struct{}

type countingRows struct{}

type countingResult struct{ affected int64 }

var (
	execMu    sync.Mutex
	execCalls []string
	// execAffected is what the next ExecContext reports as rows
	// affected. Tests set it to model a partial dedupe.
	execAffected int64
)

func recordExec(query string) {
	execMu.Lock()
	defer execMu.Unlock()
	execCalls = append(execCalls, query)
}

func resetExecCalls(affected int64) {
	execMu.Lock()
	defer execMu.Unlock()
	execCalls = nil
	execAffected = affected
}

func execSnapshot() []string {
	execMu.Lock()
	defer execMu.Unlock()
	return append([]string(nil), execCalls...)
}

func (countingDriver) Open(string) (driver.Conn, error) { return countingConn{}, nil }

func (countingConn) Prepare(string) (driver.Stmt, error) { return countingStmt{}, nil }
func (countingConn) Close() error                        { return nil }
func (countingConn) Begin() (driver.Tx, error)           { return countingTx{}, nil }

func (countingConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	recordExec(query)
	execMu.Lock()
	affected := execAffected
	execMu.Unlock()
	return countingResult{affected: affected}, nil
}

func (countingConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (countingStmt) Close() error                               { return nil }
func (countingStmt) NumInput() int                              { return -1 }
func (countingStmt) Exec([]driver.Value) (driver.Result, error) { return countingResult{}, nil }
func (countingStmt) Query([]driver.Value) (driver.Rows, error)  { return countingRows{}, nil }

func (countingTx) Commit() error   { return nil }
func (countingTx) Rollback() error { return nil }

func (countingRows) Columns() []string         { return nil }
func (countingRows) Close() error              { return nil }
func (countingRows) Next([]driver.Value) error { return io.EOF }

func (r countingResult) LastInsertId() (int64, error) { return 1, nil }
func (r countingResult) RowsAffected() (int64, error) { return r.affected, nil }

var registerCountingOnce sync.Once

func countingDB(t *testing.T) *sql.DB {
	t.Helper()
	registerCountingOnce.Do(func() { sql.Register("notification-counting", countingDriver{}) })
	db, err := sql.Open("notification-counting", "")
	if err != nil {
		t.Fatalf("open counting db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func batchOf(n int) notificationBatch {
	rows := make([]notificationRow, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, notificationRow{
			publicID:    types.New(),
			recipientID: uint32(i + 1), //#nosec G115 -- test fixture indices
			channel:     generated.NotificationsChannelInApp,
		})
	}
	return notificationBatch{
		rows:         rows,
		workspaceID:  1,
		eventType:    "task.created",
		resourceType: "task",
		title:        "A new task was created",
		severity:     generated.NotificationsSeverityNormal,
	}
}

// TestFanOutWritesOneStatementPerChunk is the regression for the
// round-trip storm. A notification for a hundred-member workspace used
// to be a hundred sequential INSERTs, each a full round trip, inside a
// goroutine holding a pooled connection for the duration. Several
// events at once and the pool is what runs out — and the symptom shows
// up in unrelated request handlers, not here.
func TestFanOutWritesOneStatementPerChunk(t *testing.T) {
	db := countingDB(t)
	f := &Fanout{db: db}

	resetExecCalls(100)
	f.insertNotifications(context.Background(), batchOf(100))

	calls := execSnapshot()
	if len(calls) != 1 {
		t.Fatalf("100 recipients must be written in one statement, sent %d", len(calls))
	}
	if got := strings.Count(calls[0], "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"); got != 100 {
		t.Fatalf("the statement must carry every row: %d value tuples, want 100", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(calls[0]), "INSERT IGNORE") {
		t.Fatalf("the insert must stay INSERT IGNORE so the unique key keeps deduping: %s", calls[0])
	}
}

// TestFanOutChunksLargeBatches keeps the statement bounded. A workspace
// with thousands of members must not become one enormous statement
// holding row locks on every notification at once.
func TestFanOutChunksLargeBatches(t *testing.T) {
	db := countingDB(t)
	f := &Fanout{db: db}

	resetExecCalls(insertChunkSize)
	f.insertNotifications(context.Background(), batchOf(insertChunkSize*2+1))

	calls := execSnapshot()
	if len(calls) != 3 {
		t.Fatalf("201 rows at chunk size %d must be 3 statements, sent %d", insertChunkSize, len(calls))
	}
	if got := strings.Count(calls[2], "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"); got != 1 {
		t.Fatalf("the final chunk must carry the remainder: %d tuples, want 1", got)
	}
}

// TestFanOutSendsNothingForEmptyBatch guards the degenerate case: an
// event whose recipients all filtered out must not produce a statement
// with no values, which is a syntax error rather than a no-op.
func TestFanOutSendsNothingForEmptyBatch(t *testing.T) {
	db := countingDB(t)
	f := &Fanout{db: db}

	resetExecCalls(0)
	f.insertNotifications(context.Background(), batchOf(0))

	if calls := execSnapshot(); len(calls) != 0 {
		t.Fatalf("an empty batch must send nothing, sent %d statements", len(calls))
	}
}
