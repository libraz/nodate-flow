package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/storagegc"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// keyFlakyRemover fails the first removal of one key and delegates every
// other call, so other queued keys a parallel test left cannot absorb
// the injected failure.
type keyFlakyRemover struct {
	inner  storagegc.ObjectRemover
	key    string
	failed bool
}

func (f *keyFlakyRemover) RemoveObject(ctx context.Context, key string) error {
	if key == f.key && !f.failed {
		f.failed = true
		return context.DeadlineExceeded
	}
	return f.inner.RemoveObject(ctx, key)
}

// queuedRemovalCount counts the storage_blob_removals records naming key.
func queuedRemovalCount(ctx context.Context, t *testing.T, key string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM storage_blob_removals WHERE storage_key = ?`, key).Scan(&n))
	return n
}

// TestSweeperRetriesAQueuedBlobUntilItIsGone holds the teardown half of
// the storage invariant: a blob whose rows are already deleted is named
// by its queued record, a failed removal keeps that record, and the next
// pass removes the blob and only then drops the record.
func TestSweeperRetriesAQueuedBlobUntilItIsGone(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()
	lockStorageSweepPass(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	key := "workspace/backrest-queued/" + randomHex(8)
	require.NoError(t, testStorage.PutBytes(ctx, key, []byte("orphan"), "text/plain"))
	_, err := helpers.ExecRetry(ctx, testDB, "test seed: queued removal",
		`INSERT INTO storage_blob_removals (storage_key) VALUES (?)`, key)
	require.NoError(t, err)

	sweeper := newSweeper(t)
	sweeper.ReservationTTL = time.Hour
	sweeper.Storage = &keyFlakyRemover{inner: testStorage.Flow, key: key}

	_, err = sweeper.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, queuedRemovalCount(ctx, t, key), "a failed removal must keep the record naming the key")
	testStorage.MustExist(t, key)

	_, err = sweeper.RunOnce(ctx)
	require.NoError(t, err)
	testStorage.MustNotExist(t, key)
	require.Zero(t, queuedRemovalCount(ctx, t, key), "the record goes once the blob is gone")
}
