package storagegc

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// Queue records key in storage_blob_removals. Call it inside the
// transaction that deletes the storage_objects row naming the blob, so
// the commit that forgets the row also commits the record of what is
// left to remove.
func Queue(ctx context.Context, qtx *generated.Queries, key string) error {
	return qtx.QueueStorageBlobRemoval(ctx, key)
}

// RemoveQueued removes a blob [Queue] recorded, once its transaction has
// committed, and drops the record after the object store confirms. A
// failure is returned and leaves the record for the sweeper to retry.
func RemoveQueued(ctx context.Context, q *generated.Queries, store ObjectRemover, key string) error {
	if err := store.RemoveObject(ctx, key); err != nil {
		return err
	}
	// affected-rows: not-applicable — the sweeper may have drained the
	// record first; the blob is gone either way.
	_, err := q.DeleteStorageBlobRemovalsByKey(ctx, []string{key})
	return err
}
