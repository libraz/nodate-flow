package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
)

// bumpFailedByID is tested via a fake DBTX that captures the exec args
// rather than hitting a real database. This validates the lockout
// threshold logic without a full handler roundtrip.

// fakeDBTX implements generated.DBTX and records exec calls.
type fakeDBTX struct {
	execCalls []execCall
}

type execCall struct {
	query string
	args  []any
}

func (f *fakeDBTX) ExecContext(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	f.execCalls = append(f.execCalls, execCall{query: query, args: args})
	return fakeResult{}, nil
}

func (f *fakeDBTX) PrepareContext(_ context.Context, _ string) (*sql.Stmt, error) {
	return nil, nil
}

func (f *fakeDBTX) QueryContext(_ context.Context, _ string, _ ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (f *fakeDBTX) QueryRowContext(_ context.Context, _ string, _ ...interface{}) *sql.Row {
	return nil
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

func TestBumpFailedByID_IncrementsCounter(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	q := generated.New(db)
	deps := Deps{Queries: q}

	bumpFailedByID(context.Background(), deps, 42, 0)

	require.Len(t, db.execCalls, 1, "must call UpdateIdentityFailedAttempts")
	args := db.execCalls[0].args
	// args: failed_attempts, locked_until_at, id
	require.Len(t, args, 3)
	assert.Equal(t, uint32(1), args[0], "failed_attempts must be incremented to 1")
	lock, ok := args[1].(sql.NullTime)
	require.True(t, ok)
	assert.False(t, lock.Valid, "must NOT set lockout for first failure")
	assert.Equal(t, uint32(42), args[2], "must target correct identity ID")
}

func TestBumpFailedByID_LocksAtThreshold(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	q := generated.New(db)
	deps := Deps{Queries: q}

	// Simulate 4 prior failures; next bump (the 5th) should trigger lockout.
	bumpFailedByID(context.Background(), deps, 42, maxFailedBeforeLock-1)

	require.Len(t, db.execCalls, 1)
	args := db.execCalls[0].args
	assert.Equal(t, uint32(maxFailedBeforeLock), args[0],
		"failed_attempts must be set to the threshold")
	lock, ok := args[1].(sql.NullTime)
	require.True(t, ok)
	assert.True(t, lock.Valid, "must set lockout when threshold reached")
	assert.True(t, lock.Time.After(time.Now().Add(14*time.Minute)),
		"lockout must be at least 14 minutes in the future")
}

func TestBumpFailedByID_AboveThresholdStillLocks(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	q := generated.New(db)
	deps := Deps{Queries: q}

	bumpFailedByID(context.Background(), deps, 42, maxFailedBeforeLock+5)

	require.Len(t, db.execCalls, 1)
	args := db.execCalls[0].args
	lock, ok := args[1].(sql.NullTime)
	require.True(t, ok)
	assert.True(t, lock.Valid, "must set lockout even when already above threshold")
}

func TestMaxFailedBeforeLock_IsReasonable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint32(5), uint32(maxFailedBeforeLock),
		"lockout threshold must be 5 to match security requirements")
}
