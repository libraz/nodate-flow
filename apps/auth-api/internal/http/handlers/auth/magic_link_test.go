package auth

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
)

// TestMarkMagicLinkUsed_QueryIsAtomicConditional pins the generated SQL
// for MarkMagicLinkUsed to its conditional form. The query MUST update
// only when used_at IS NULL so two concurrent verifies can never both
// stamp the same row. If anyone reverts the query to an unconditional
// UPDATE the race window reopens and a single magic link could mint two
// sessions; this guard fails fast at compile-test time.
func TestMarkMagicLinkUsed_QueryIsAtomicConditional(t *testing.T) {
	t.Parallel()
	rec := &queryCaptureDBTX{rowsAffected: 1}
	q := generated.New(rec)

	_, err := q.MarkMagicLinkUsed(context.Background(), 42)
	require.NoError(t, err)

	require.Len(t, rec.execCalls, 1, "must execute exactly one UPDATE")
	got := rec.execCalls[0].query
	assert.Contains(t, strings.ToUpper(got), "UPDATE MAGIC_LINK_TOKENS")
	assert.Contains(t, strings.ToUpper(got), "USED_AT IS NULL",
		"WHERE clause must include 'used_at IS NULL' for atomic CAS; "+
			"without it, two concurrent verify requests could both succeed")
}

// TestMarkMagicLinkUsed_ConcurrentRaceOnlyOneWins simulates the database
// behaviour of the conditional UPDATE: the first matching execution
// returns RowsAffected=1, every subsequent execution against the same
// row returns 0. The handler treats 0 as "already consumed". This test
// proves that under N concurrent goroutines hitting MarkMagicLinkUsed,
// exactly one observes affected==1 — i.e. only one verify can ever
// mint a session.
func TestMarkMagicLinkUsed_ConcurrentRaceOnlyOneWins(t *testing.T) {
	t.Parallel()
	rec := &casDBTX{}
	q := generated.New(rec)

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([]int64, goroutines)
	for i := 0; i < goroutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			affected, err := q.MarkMagicLinkUsed(context.Background(), 42)
			require.NoError(t, err)
			results[idx] = affected
		}()
	}
	wg.Wait()

	winners := 0
	for _, r := range results {
		if r == 1 {
			winners++
		}
	}
	assert.Equal(t, 1, winners,
		"exactly one concurrent caller must see RowsAffected=1; "+
			"others must see 0 and be rejected as ALREADY_USED")
	assert.Equal(t, int32(goroutines), rec.calls.Load(),
		"every goroutine must have actually executed the UPDATE")
}

// queryCaptureDBTX records every Exec query and returns a configurable
// RowsAffected value. Used by the SQL pinning test.
type queryCaptureDBTX struct {
	mu           sync.Mutex
	execCalls    []capturedExec
	rowsAffected int64
}

type capturedExec struct {
	query string
	args  []any
}

func (f *queryCaptureDBTX) ExecContext(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	f.mu.Lock()
	f.execCalls = append(f.execCalls, capturedExec{query: query, args: args})
	f.mu.Unlock()
	return staticResult{rowsAffected: f.rowsAffected}, nil
}

func (f *queryCaptureDBTX) PrepareContext(_ context.Context, _ string) (*sql.Stmt, error) {
	return nil, nil
}

func (f *queryCaptureDBTX) QueryContext(_ context.Context, _ string, _ ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (f *queryCaptureDBTX) QueryRowContext(_ context.Context, _ string, _ ...interface{}) *sql.Row {
	return nil
}

// casDBTX simulates the conditional UPDATE: the first ExecContext
// returns RowsAffected=1, every later call returns 0. Exec is the only
// method the test needs.
type casDBTX struct {
	calls atomic.Int32
}

func (f *casDBTX) ExecContext(_ context.Context, _ string, _ ...interface{}) (sql.Result, error) {
	n := f.calls.Add(1)
	if n == 1 {
		return staticResult{rowsAffected: 1}, nil
	}
	return staticResult{rowsAffected: 0}, nil
}

func (f *casDBTX) PrepareContext(_ context.Context, _ string) (*sql.Stmt, error) {
	return nil, nil
}

func (f *casDBTX) QueryContext(_ context.Context, _ string, _ ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (f *casDBTX) QueryRowContext(_ context.Context, _ string, _ ...interface{}) *sql.Row {
	return nil
}

type staticResult struct {
	rowsAffected int64
}

func (s staticResult) LastInsertId() (int64, error) { return 0, nil }
func (s staticResult) RowsAffected() (int64, error) { return s.rowsAffected, nil }
