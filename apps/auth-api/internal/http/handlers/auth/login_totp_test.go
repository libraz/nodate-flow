package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
)

// The fake DBTX below captures exec args so the handler-side wiring
// (which threshold, which identity) can be checked without a database.
// What the statement itself does with those args is covered against a
// real MySQL in lockout_concurrency_test.go, because the property that
// matters — the count advancing once per attempt when attempts overlap
// — cannot be observed through a fake.

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

// TestBumpFailedByID_PassesTheTotpThreshold checks the wiring the
// handler is still responsible for: which threshold and which identity
// the statement is asked to apply. The count itself is no longer the
// caller's to compute — it is incremented inside the statement so
// simultaneous attempts cannot share a starting value — so there is
// nothing here to assert about it, and the behaviour that replaced it
// is covered against a real database in lockout_concurrency_test.go.
func TestBumpFailedByID_PassesTheTotpThreshold(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	deps := Deps{Queries: generated.New(db)}

	bumpFailedByID(context.Background(), deps, 42)

	require.Len(t, db.execCalls, 1, "must call BumpIdentityFailedAttempts")
	args := db.execCalls[0].args
	// args: threshold, locked_until, id
	require.Len(t, args, 3)
	assert.Equal(t, uint32(maxFailedBeforeLock), args[0],
		"the TOTP path must apply the TOTP threshold")
	lock, ok := args[1].(sql.NullTime)
	require.True(t, ok)
	assert.True(t, lock.Valid,
		"a deadline is always supplied; the statement decides whether the count has reached the threshold")
	assert.True(t, lock.Time.After(time.Now().Add(lockoutWindow-time.Minute)),
		"the deadline must be a full lockout window away")
	assert.Equal(t, uint32(42), args[2], "must target the right identity")
}

// TestBumpFailedByIDWithThreshold_PassesTheRecoveryThreshold is the
// wire-up test for the tighter recovery budget: without it the recovery
// branch would inherit the laxer TOTP threshold and hand an attacker
// five times the attempts per lockout period.
func TestBumpFailedByIDWithThreshold_PassesTheRecoveryThreshold(t *testing.T) {
	t.Parallel()
	db := &fakeDBTX{}
	deps := Deps{Queries: generated.New(db)}

	bumpFailedByIDWithThreshold(context.Background(), deps, 42, maxRecoveryFailedBeforeLock)

	require.Len(t, db.execCalls, 1)
	assert.Equal(t, uint32(maxRecoveryFailedBeforeLock), db.execCalls[0].args[0],
		"the recovery branch must apply its own threshold")
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
