package reconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// deadlock is the transient error InnoDB returns when it picks this
// statement as the victim of a lock cycle.
func deadlock() error {
	return &mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock; try restarting transaction"}
}

// TestHealRetriesTransientErrors covers the retry the healing path
// depends on.
//
// A heal that loses a lock race is otherwise logged and dropped, and
// the pair stays broken until some later pass happens to catch it —
// which makes recovery a property of the schedule rather than of the
// reconciler, and leaves the one-shot callers with no recovery at all.
//
// The assertion is on the seam rather than on a real contended table:
// making MySQL hand back a deadlock on demand needs control of both
// transactions in the cycle, and the heal's is opened inside the
// driver. What can be pinned down deterministically is that the healing
// path re-issues the statement when the error says it is worth
// re-issuing, and gives up on one that does not.
func TestHealRetriesTransientErrors(t *testing.T) {
	t.Parallel()

	r := &Reconciler{}
	calls := 0
	err := r.heal(context.Background(), "test.heal", func(context.Context) error {
		calls++
		if calls < 3 {
			return deadlock()
		}
		return nil
	})

	require.NoError(t, err, "a heal that succeeds on retry must be reported as healed")
	require.Equal(t, 3, calls, "a deadlocked heal must be re-issued, not dropped")
}

// TestHealDoesNotRetryPermanentErrors is the other half: a statement
// that failed for a reason retrying cannot change must fail once.
// Retrying it would multiply the damage of a genuine bug and bury the
// original error under two more.
func TestHealDoesNotRetryPermanentErrors(t *testing.T) {
	t.Parallel()

	wanted := errors.New("column does not exist")
	r := &Reconciler{}
	calls := 0
	err := r.heal(context.Background(), "test.heal", func(context.Context) error {
		calls++
		return wanted
	})

	require.ErrorIs(t, err, wanted)
	require.Equal(t, 1, calls, "a permanent failure must not be re-issued")
}
