package auth

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
)

// TestFailedAttemptsSurviveConcurrentGuesses is the regression for a
// lockout that could be defeated by sending the guesses at once.
//
// The counter used to be read by the handler, incremented in Go and
// written back as an absolute value. Guesses that overlapped therefore
// all read the same number and all wrote that number plus one: the
// stored count went to 1 and stayed there no matter how many attempts
// arrived, and the threshold was never reached. Since the per-account
// budget is the only limit on online guessing — the rate limiter is
// per-IP and process-local — an attacker spreading requests across
// addresses was bounded by nothing but the cost of Argon2id.
//
// The test therefore fires the attempts concurrently on purpose. A
// sequential version passes against the broken code.
func TestFailedAttemptsSurviveConcurrentGuesses(t *testing.T) {
	db := requireB2DB(t)
	t.Parallel()

	ctx := context.Background()
	q := generated.New(db)
	uid, _, _ := b2NewUser(t, q)
	ident, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)

	const attempts = maxFailedBeforeLock
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = q.BumpIdentityFailedAttempts(ctx, generated.BumpIdentityFailedAttemptsParams{
				Threshold:   maxFailedBeforeLock,
				LockedUntil: nullTimeAt(time.Now().Add(lockoutWindow)),
				ID:          ident.ID,
			})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	after, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, attempts, after.FailedAttempts,
		"every concurrent attempt must advance the counter, or the lockout can be outrun by sending the guesses together")
	require.True(t, after.LockedUntilAt.Valid,
		"reaching the threshold must lock the account whichever attempt gets there")
	require.True(t, after.LockedUntilAt.Time.After(time.Now().Add(lockoutWindow-time.Minute)),
		"the lock must run for the full window")
}

// TestFailedAttemptsLockOnlyAtThreshold pins the boundary: the account
// stays usable until the threshold, and the deadline appears on the
// attempt that reaches it.
func TestFailedAttemptsLockOnlyAtThreshold(t *testing.T) {
	db := requireB2DB(t)
	t.Parallel()

	ctx := context.Background()
	q := generated.New(db)
	uid, _, _ := b2NewUser(t, q)
	ident, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)

	bump := func() {
		require.NoError(t, q.BumpIdentityFailedAttempts(ctx, generated.BumpIdentityFailedAttemptsParams{
			Threshold:   maxFailedBeforeLock,
			LockedUntil: nullTimeAt(time.Now().Add(lockoutWindow)),
			ID:          ident.ID,
		}))
	}

	for i := uint32(1); i < maxFailedBeforeLock; i++ {
		bump()
		row, err := q.FindLocalIdentityByUserId(ctx, uid)
		require.NoError(t, err)
		require.EqualValues(t, i, row.FailedAttempts)
		require.Falsef(t, row.LockedUntilAt.Valid,
			"attempt %d is below the threshold and must not lock the account", i)
	}

	bump()
	row, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, maxFailedBeforeLock, row.FailedAttempts)
	require.True(t, row.LockedUntilAt.Valid, "the attempt that reaches the threshold must lock")
}

// TestLockedAccountKeepsItsOriginalDeadline is the regression for a
// lockout that could be held down from outside.
//
// The counter only clears on a successful authentication, which a
// locked-out owner cannot perform. So if every further failure moved
// the deadline, anyone able to send a failed login could keep the
// account shut for as long as they cared to, one guess per window,
// without ever learning the password — the mechanism meant to slow
// guessing would instead hand out the ability to take an account away
// from its owner.
func TestLockedAccountKeepsItsOriginalDeadline(t *testing.T) {
	db := requireB2DB(t)
	t.Parallel()

	ctx := context.Background()
	q := generated.New(db)
	uid, _, _ := b2NewUser(t, q)
	ident, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)

	for range maxFailedBeforeLock {
		bumpOnce(t, q, ident.ID, maxFailedBeforeLock, lockoutWindow)
	}
	locked, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.True(t, locked.LockedUntilAt.Valid, "the threshold must lock the account")
	deadline := locked.LockedUntilAt.Time

	// Keep guessing, asking for a much longer window each time.
	for range 3 {
		bumpOnce(t, q, ident.ID, maxFailedBeforeLock, 8*lockoutWindow)
	}

	after, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.WithinDuration(t, deadline, after.LockedUntilAt.Time, time.Second,
		"failures during a lock must not move its deadline, or the lock can be held down indefinitely")
	require.Greater(t, after.FailedAttempts, uint32(maxFailedBeforeLock),
		"the attempts still count; only the deadline is fixed")
}

// TestExpiredLockGivesBackAFullAllowance is the other half. Carrying the
// old count past the expiry would leave the owner one failure away from
// being locked again for as long as the attacker keeps trying, so the
// threshold would promise attempts it never actually gives.
func TestExpiredLockGivesBackAFullAllowance(t *testing.T) {
	db := requireB2DB(t)
	t.Parallel()

	ctx := context.Background()
	q := generated.New(db)
	uid, _, _ := b2NewUser(t, q)
	ident, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)

	for range maxFailedBeforeLock {
		bumpOnce(t, q, ident.ID, maxFailedBeforeLock, lockoutWindow)
	}
	// Let the window lapse.
	_, err = db.ExecContext(ctx,
		"UPDATE identities SET locked_until_at = ? WHERE id = ?",
		time.Now().Add(-time.Minute), ident.ID)
	require.NoError(t, err)

	// The first failure of the new window starts the count over.
	bumpOnce(t, q, ident.ID, maxFailedBeforeLock, lockoutWindow)
	row, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, 1, row.FailedAttempts,
		"a failure after the window has lapsed begins a new count")
	require.False(t, row.LockedUntilAt.Valid,
		"one failure must not re-lock an account that has served its window")

	// And the allowance really is the full threshold again.
	for i := uint32(2); i < maxFailedBeforeLock; i++ {
		bumpOnce(t, q, ident.ID, maxFailedBeforeLock, lockoutWindow)
		row, err := q.FindLocalIdentityByUserId(ctx, uid)
		require.NoError(t, err)
		require.Falsef(t, row.LockedUntilAt.Valid,
			"attempt %d of the new window is below the threshold", i)
	}
	bumpOnce(t, q, ident.ID, maxFailedBeforeLock, lockoutWindow)
	row, err = q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.True(t, row.LockedUntilAt.Valid,
		"the threshold still locks once the new window reaches it")
}

// TestRecoveryCodeThresholdLocksEarlier proves the tighter budget the
// recovery-code branch asks for actually takes effect in the statement,
// not just in the constant.
func TestRecoveryCodeThresholdLocksEarlier(t *testing.T) {
	db := requireB2DB(t)
	t.Parallel()

	ctx := context.Background()
	q := generated.New(db)
	uid, _, _ := b2NewUser(t, q)
	ident, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)

	for range maxRecoveryFailedBeforeLock {
		require.NoError(t, q.BumpIdentityFailedAttempts(ctx, generated.BumpIdentityFailedAttemptsParams{
			Threshold:   maxRecoveryFailedBeforeLock,
			LockedUntil: nullTimeAt(time.Now().Add(lockoutWindow)),
			ID:          ident.ID,
		}))
	}

	row, err := q.FindLocalIdentityByUserId(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, maxRecoveryFailedBeforeLock, row.FailedAttempts)
	require.True(t, row.LockedUntilAt.Valid,
		"the recovery threshold must lock at its own count, not at the TOTP one")
}

// nullTimeAt wraps a deadline for the lockout parameter.
func nullTimeAt(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: true}
}

// bumpOnce records one failed authentication.
func bumpOnce(t *testing.T, q *generated.Queries, identityID, threshold uint32, window time.Duration) {
	t.Helper()
	require.NoError(t, q.BumpIdentityFailedAttempts(context.Background(), generated.BumpIdentityFailedAttemptsParams{
		Threshold:   threshold,
		LockedUntil: nullTimeAt(time.Now().Add(window)),
		ID:          identityID,
	}))
}
