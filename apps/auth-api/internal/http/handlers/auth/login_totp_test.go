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

// TestRecoveryCodeThreshold_IsTighter pins the invariant that recovery
// codes lock out faster than TOTP codes. Recovery codes are high-entropy
// single-use secrets, so a small attempt budget caps the brute-force
// success probability before a determined attacker can search the space.
func TestRecoveryCodeThreshold_IsTighter(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint32(3), uint32(maxRecoveryFailedBeforeLock),
		"recovery-code lockout threshold must be 3")
	assert.Less(t, uint32(maxRecoveryFailedBeforeLock), uint32(maxFailedBeforeLock),
		"recovery threshold must be strictly tighter than TOTP threshold")
}

// TestBumpFailedByIDWithThreshold_LocksAtRecoveryThreshold validates the
// recovery-code branch trips the lockout at three failures even though
// the TOTP threshold is five. This is the wire-up test for the L1 fix:
// without it, recovery codes would inherit the laxer TOTP threshold and
// give an attacker five times the attempt budget per period.
func TestBumpFailedByIDWithThreshold_LocksAtRecoveryThreshold(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	q := generated.New(db)
	deps := Deps{Queries: q}

	// Two prior failures; the third (this call) should trigger lockout
	// when the recovery threshold is in effect.
	bumpFailedByIDWithThreshold(context.Background(), deps, 42, maxRecoveryFailedBeforeLock-1, maxRecoveryFailedBeforeLock)

	require.Len(t, db.execCalls, 1)
	args := db.execCalls[0].args
	assert.Equal(t, uint32(maxRecoveryFailedBeforeLock), args[0],
		"failed_attempts must reach the recovery threshold")
	lock, ok := args[1].(sql.NullTime)
	require.True(t, ok)
	assert.True(t, lock.Valid,
		"recovery threshold must lock at 3 even though TOTP threshold is 5")
}

// TestBumpFailedByIDWithThreshold_DoesNotLockBelowTotpThreshold confirms
// the legacy TOTP path (called via [bumpFailedByID]) does NOT lock at 3
// failures — the threshold parameter is what gates the lockout, not a
// global rule.
func TestBumpFailedByIDWithThreshold_DoesNotLockBelowTotpThreshold(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	q := generated.New(db)
	deps := Deps{Queries: q}

	// Three prior failures with TOTP threshold (5) — should NOT lock.
	bumpFailedByID(context.Background(), deps, 42, 2)

	require.Len(t, db.execCalls, 1)
	args := db.execCalls[0].args
	assert.Equal(t, uint32(3), args[0])
	lock, ok := args[1].(sql.NullTime)
	require.True(t, ok)
	assert.False(t, lock.Valid,
		"TOTP path must not lock at 3 failures — that is the recovery-only threshold")
}
