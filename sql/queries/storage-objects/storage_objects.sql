-- name: FindStorageObjectByWorkspaceSha :one
-- Look up a workspace-scoped storage object by its content hash.
-- Used at upload time to detect a dedup hit and bump ref_count instead
-- of inserting a new row. Matches the (workspace_id, sha256) UNIQUE key.
SELECT
  id,
  public_id,
  workspace_id,
  owner_user_id,
  sha256,
  byte_size,
  content_type,
  storage_key,
  ref_count,
  enabled,
  updated_at,
  created_at
FROM storage_objects
WHERE workspace_id = ?
  AND sha256 = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindStorageObjectByOwnerUserSha :one
-- Look up a user-scoped storage object (e.g. avatar) by content hash.
-- Used at upload time so that re-uploading the same avatar bytes reuses
-- the existing object instead of allocating a fresh row in MinIO.
SELECT
  id,
  public_id,
  workspace_id,
  owner_user_id,
  sha256,
  byte_size,
  content_type,
  storage_key,
  ref_count,
  enabled,
  updated_at,
  created_at
FROM storage_objects
WHERE owner_user_id = ?
  AND sha256 = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindStorageObjectByPublicID :one
-- Resolve a storage object by its externally visible UUID v7.
-- The public_id is what handlers receive from the SDK; internal id is
-- only ever exchanged via the FK on the referencing rows.
SELECT
  id,
  public_id,
  workspace_id,
  owner_user_id,
  sha256,
  byte_size,
  content_type,
  storage_key,
  ref_count,
  enabled,
  updated_at,
  created_at
FROM storage_objects
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindStorageObjectByID :one
-- Resolve a storage object by its internal id. Used inside the same
-- transaction as the referencing row insert/delete, where the FK id is
-- already known and a public_id round trip is unnecessary.
SELECT
  id,
  public_id,
  workspace_id,
  owner_user_id,
  sha256,
  byte_size,
  content_type,
  storage_key,
  ref_count,
  enabled,
  updated_at,
  created_at
FROM storage_objects
WHERE id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: InsertStorageObject :execresult
-- Create a brand new storage object row with ref_count = 1, representing
-- the first reference (the just-inserted attachment / avatar). Caller
-- supplies a UUID v7 public_id and either workspace_id OR owner_user_id
-- (the CHECK constraint enforces exactly one is non-null).
INSERT INTO storage_objects (
  public_id,
  workspace_id,
  owner_user_id,
  sha256,
  byte_size,
  content_type,
  storage_key,
  ref_count
) VALUES (?, ?, ?, ?, ?, ?, ?, 1);

-- name: IncrementStorageObjectRefCount :exec
-- Bump ref_count by 1 when an additional referencing row is created
-- (e.g. dedup hit on a re-upload). Caller MUST run this inside the
-- same transaction as the referencing INSERT so the count cannot drift
-- if the INSERT fails.
UPDATE storage_objects
SET ref_count = ref_count + 1
WHERE id = ?;

-- name: DecrementStorageObjectRefCount :execresult
-- Atomically drop ref_count by 1 with an underflow guard. Returning
-- sql.Result lets the caller assert RowsAffected() == 1; a zero result
-- means the row was already at 0 (programmer error: too many decrements)
-- and the caller should rollback rather than continue.
UPDATE storage_objects
SET ref_count = ref_count - 1
WHERE id = ?
  AND ref_count > 0;

-- name: DeleteStorageObjectIfUnreferenced :execresult
-- Hard-delete a storage object row only if no referencing rows remain.
-- The WHERE ref_count = 0 makes this race-safe against a concurrent
-- IncrementStorageObjectRefCount: if another transaction grabbed the
-- row for dedup just before us, RowsAffected() returns 0 and the GC
-- sweeper leaves the underlying MinIO blob alone.
DELETE FROM storage_objects
WHERE id = ?
  AND ref_count = 0;
