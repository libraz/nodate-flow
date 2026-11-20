-- name: QueueStorageBlobRemoval :exec
-- Record a key whose blob must be removed once the rows naming it are
-- gone. Written inside the teardown transaction that deletes those rows,
-- so the record commits with the delete. A key already queued stays one
-- row.
INSERT INTO storage_blob_removals (storage_key)
VALUES (?)
ON DUPLICATE KEY UPDATE storage_key = VALUES(storage_key);

-- name: ListStorageBlobRemovals :many
-- Oldest queued removals first, one sweeper batch at a time.
SELECT
  id,
  storage_key
FROM storage_blob_removals
ORDER BY id ASC
LIMIT ?;

-- name: DeleteStorageBlobRemoval :execrows
-- Drop a queued removal once its blob is gone from object storage.
DELETE FROM storage_blob_removals
WHERE id = ?;

-- name: DeleteStorageBlobRemovalsByKey :execrows
-- Drop the queued removals for keys a teardown has just removed from
-- object storage itself.
DELETE FROM storage_blob_removals
WHERE storage_key IN (sqlc.slice('storage_keys'));

-- name: CountStorageObjectsHoldingKey :one
-- How many storage_objects rows hold a key: confirmed ones vouch for the
-- bytes there, and a referenced reservation is an upload in flight. A
-- reservation nothing references -- one whose bytes failed their digest --
-- holds nothing. A drainer that finds a holder leaves the blob to it.
SELECT COUNT(*) AS held
FROM storage_objects
WHERE storage_key = ?
  AND (uploaded_at IS NOT NULL OR ref_count > 0);
