package storage

import (
	"context"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
)

// QueueRemoval records key in storage_blob_removals. Call it inside the
// transaction that deletes the storage_objects row naming the blob, so
// the commit that forgets the row also commits the record of what is
// left to remove; the flow-api storage sweeper retries it until the
// object store confirms.
func QueueRemoval(ctx context.Context, qtx *generated.Queries, key string) error {
	return qtx.QueueStorageBlobRemoval(ctx, key)
}

// RemoveQueued removes a blob [QueueRemoval] recorded, once its
// transaction has committed, and drops the record after the object store
// confirms. A failure is returned and leaves the record for the sweeper.
func RemoveQueued(ctx context.Context, q *generated.Queries, c *Client, key string) error {
	if err := c.RemoveObject(ctx, key); err != nil {
		return err
	}
	// affected-rows: not-applicable — the sweeper may have drained the
	// record first; the blob is gone either way.
	_, err := q.DeleteStorageBlobRemovalsByKey(ctx, []string{key})
	return err
}
