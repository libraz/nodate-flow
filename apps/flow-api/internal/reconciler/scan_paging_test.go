package reconciler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The reconciler runs on every replica, against every tenant's rows, on
// a five-minute timer. Its cost per pass is therefore a property of the
// deployment rather than of any request, and the failure it guards
// against is not a wrong answer but an unbounded one: a scan with no
// LIMIT reads the whole table and buffers whatever it matched, and both
// grow with the largest customer.
//
// The tests below drive the scans against a stub database/sql driver
// rather than MySQL. What they can pin down that way is exactly what
// went wrong — every scan asks for a bounded page, the page size is the
// declared one, and the cursor advances so successive passes cover the
// table instead of re-reading its head forever. Whether the server can
// answer those queries from an index is a question for EXPLAIN, not for
// a stub, and it is why idx_tasks_enabled_id exists.

// TestEveryScanAsksForABoundedPage fails if any scan drops its LIMIT.
func TestEveryScanAsksForABoundedPage(t *testing.T) {
	t.Parallel()

	db, rec := newStubReconciler(t, func(string) int { return 0 })
	defer db.Close()

	rec.RunOnce(context.Background())

	queries := rec.stub.selects()
	require.Len(t, queries, 3, "a pass runs three scans; each must issue exactly one paged read")
	for _, q := range queries {
		require.Contains(t, q.sql, "LIMIT ?",
			"an unpaged scan grows with the largest tenant's table: %s", q.sql)
		require.Equal(t, int64(maxScanRowsPerPass), q.args[len(q.args)-1],
			"the page size must be maxScanRowsPerPass: %s", q.sql)
	}
}

// TestEnabledMismatchScanDrivesOffTheDisabledSide pins the sargable
// shape. Asking calendar_events for every row and then checking each
// row's task reads the whole event table to find a handful of drifted
// pairs; asking tasks for the disabled ones reads only those. The
// difference is 125 ms versus 7 ms on 400k tasks / 200k events, and it
// is the reason idx_tasks_enabled_id was added.
func TestEnabledMismatchScanDrivesOffTheDisabledSide(t *testing.T) {
	t.Parallel()

	db, rec := newStubReconciler(t, func(string) int { return 0 })
	defer db.Close()

	rec.RunOnce(context.Background())

	q := rec.stub.selectContaining(t, "enabled = FALSE")
	require.Contains(t, q, "FROM tasks t",
		"the disabled side of tasks is the small side; it has to drive the join")
	require.Contains(t, q, "ORDER BY t.id",
		"the resume cursor has to ride the same index as the predicate")
}

// TestFullPageResumesWhereItStopped fails if a scan re-reads the head of
// the table every pass. A cursor that does not advance turns the whole
// paging exercise into a scan that can never reach the rest of the
// table, so drift past the first page is never found.
func TestFullPageResumesWhereItStopped(t *testing.T) {
	t.Parallel()

	db, rec := newStubReconciler(t, func(string) int { return maxScanRowsPerPass })
	defer db.Close()

	rec.RunOnce(context.Background())
	first := rec.stub.selects()
	rec.stub.reset()
	rec.RunOnce(context.Background())
	second := rec.stub.selects()

	require.Len(t, second, 3)
	for i, q := range second {
		require.Equal(t, int64(maxScanRowsPerPass), cursorArg(q),
			"pass 2 of %s must resume from the last id pass 1 saw, not from the top",
			strings.TrimSpace(first[i].sql))
	}
}

// TestShortPageRewindsToTheTop is the other half: a scan that reached
// the end of the table must start over, or drift written behind the
// cursor is never healed.
func TestShortPageRewindsToTheTop(t *testing.T) {
	t.Parallel()

	db, rec := newStubReconciler(t, func(string) int { return maxScanRowsPerPass - 1 })
	defer db.Close()

	rec.RunOnce(context.Background())
	rec.stub.reset()
	rec.RunOnce(context.Background())

	for _, q := range rec.stub.selects() {
		require.Equal(t, int64(0), cursorArg(q),
			"a short page means the end of the table; the next pass starts at the top: %s", q.sql)
	}
}

// TestAdvanceCursorWraps covers the helper directly, including the
// boundary where a page is exactly full.
func TestAdvanceCursorWraps(t *testing.T) {
	t.Parallel()

	var cur atomic.Uint32
	advanceCursor(&cur, maxScanRowsPerPass, 900)
	require.Equal(t, uint32(900), cur.Load(), "a full page has more table behind it")

	advanceCursor(&cur, maxScanRowsPerPass-1, 1200)
	require.Equal(t, uint32(0), cur.Load(), "a short page means start over")

	advanceCursor(&cur, 0, 0)
	require.Equal(t, uint32(0), cur.Load(), "an empty page is a short page")
}

// --- stub driver ------------------------------------------------------
//
// database/sql needs a driver, not a mock library, to record what a
// scan actually sent. The stub answers every scan with a page of rows
// whose values are deliberately consistent — matching dates, both link
// columns set — so nothing looks like drift and no heal runs. The
// question under test is the shape of the read, not the heal.

type recordedQuery struct {
	sql  string
	args []driver.Value
}

// cursorArg returns the keyset cursor a scan bound. Every scan ends its
// parameter list with (cursor, limit); what comes before that differs
// per scan, so the cursor is read from the end.
func cursorArg(q recordedQuery) driver.Value {
	return q.args[len(q.args)-2]
}

type stubDriver struct {
	mu      sync.Mutex
	queries []recordedQuery
	// rowsFor decides how many rows a given scan's page returns, which
	// is what makes "full page" and "short page" testable.
	rowsFor func(sql string) int
}

func (d *stubDriver) Open(string) (driver.Conn, error) { return &stubConn{d: d}, nil }

func (d *stubDriver) record(q recordedQuery) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, q)
}

func (d *stubDriver) selects() []recordedQuery {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]recordedQuery, 0, len(d.queries))
	for _, q := range d.queries {
		if strings.HasPrefix(strings.TrimSpace(q.sql), "SELECT") {
			out = append(out, q)
		}
	}
	return out
}

func (d *stubDriver) selectContaining(t *testing.T, needle string) string {
	t.Helper()
	for _, q := range d.selects() {
		if strings.Contains(q.sql, needle) {
			return q.sql
		}
	}
	t.Fatalf("no scan issued a query containing %q", needle)
	return ""
}

func (d *stubDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = nil
}

type stubConn struct{ d *stubDriver }

func (c *stubConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *stubConn) Close() error                        { return nil }
func (c *stubConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *stubConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.d.record(recordedQuery{sql: query, args: values(args)})
	return driver.RowsAffected(0), nil
}

func (c *stubConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.record(recordedQuery{sql: query, args: values(args)})
	return newStubRows(query, c.d.rowsFor(query)), nil
}

func values(args []driver.NamedValue) []driver.Value {
	out := make([]driver.Value, 0, len(args))
	for _, a := range args {
		out = append(out, a.Value)
	}
	return out
}

type stubRows struct {
	cols []string
	row  func(i int) []driver.Value
	n    int
	i    int
}

// newStubRows shapes its page to whichever scan asked for it. The ids
// count up from 1 so the last row of a full page is maxScanRowsPerPass,
// which is what the resume assertions read.
func newStubRows(query string, n int) *stubRows {
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	switch {
	case strings.Contains(query, "ce.timezone"):
		return &stubRows{
			cols: []string{"task_id", "event_id", "due_on", "start_at", "timezone"},
			n:    n,
			row: func(i int) []driver.Value {
				return []driver.Value{int64(i), int64(i), day, day, "UTC"}
			},
		}
	case strings.Contains(query, "task_role"):
		return &stubRows{
			cols: []string{"id", "public_id", "task_id", "task_role"},
			n:    n,
			row: func(i int) []driver.Value {
				return []driver.Value{int64(i), make([]byte, 16), int64(i), "due"}
			},
		}
	default:
		return &stubRows{
			cols: []string{"task_id", "event_id"},
			n:    n,
			row: func(i int) []driver.Value {
				return []driver.Value{int64(i), int64(i)}
			},
		}
	}
}

func (r *stubRows) Columns() []string { return r.cols }
func (r *stubRows) Close() error      { return nil }

func (r *stubRows) Next(dest []driver.Value) error {
	if r.i >= r.n {
		return io.EOF
	}
	r.i++
	copy(dest, r.row(r.i))
	return nil
}

type stubReconciler struct {
	*Reconciler
	stub *stubDriver
}

var stubDriverSeq atomic.Uint64

func newStubReconciler(t *testing.T, rowsFor func(sql string) int) (*sql.DB, *stubReconciler) {
	t.Helper()

	d := &stubDriver{rowsFor: rowsFor}
	// database/sql keeps a process-wide driver registry, so each test
	// needs its own name to stay parallel-safe.
	name := "reconciler-stub-" + time.Now().Format("150405.000000000") + "-" +
		string(rune('a'+stubDriverSeq.Add(1)%26))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	require.NoError(t, err)

	return db, &stubReconciler{
		Reconciler: &Reconciler{
			DB:     db,
			Logger: slog.New(slog.DiscardHandler),
		},
		stub: d,
	}
}
