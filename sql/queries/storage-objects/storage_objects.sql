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
  uploaded_at,
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
  uploaded_at,
  enabled,
  updated_at,
  created_at
FROM storage_objects
WHERE owner_user_id = ?
  AND sha256 = ?
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
  uploaded_at,
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

-- name: MarkStorageObjectUploaded :exec
-- Record that the bytes behind this row have been seen in object
-- storage and their real size checked. Until this runs the row is a
-- reservation, not a stored object: the only size anyone has is the one
-- the client declared, which is exactly the number an attacker lies
-- about. The stamp is what promotes it to a dedup candidate and takes
-- it out of the sweeper's reach.
UPDATE storage_objects
SET uploaded_at = NOW(3),
    byte_size   = sqlc.arg(byte_size)
WHERE id = sqlc.arg(id)
  AND enabled = TRUE;

-- name: ListUnconfirmedStorageObjects :many
-- Reservations whose upload never arrived. The cutoff is supplied by
-- the caller and must be past the lifetime of the presigned URL the row
-- was created for: while that URL is valid an upload can still land, so
-- reclaiming earlier would delete a row out from under a transfer in
-- progress.
SELECT
  id,
  public_id,
  workspace_id,
  storage_key,
  ref_count,
  created_at
FROM storage_objects
WHERE uploaded_at IS NULL
  AND created_at < sqlc.arg(cutoff)
  AND enabled = TRUE
ORDER BY created_at ASC
LIMIT ?;

-- name: ListUnreferencedStorageObjects :many
-- Rows nothing points at any more. The delete paths drop these inline,
-- so anything showing up here is the residue of a delete that got part
-- way — an object-store call that failed, a workspace teardown that
-- died — and would otherwise sit in the bucket forever.
SELECT
  id,
  public_id,
  workspace_id,
  storage_key,
  ref_count,
  created_at
FROM storage_objects
WHERE ref_count = 0
  AND enabled = TRUE
ORDER BY created_at ASC
LIMIT ?;

-- name: DeleteAttachmentsForStorageObject :execrows
-- Drop the attachment rows that point at a reservation being reclaimed.
-- They exist because the presign created both in one transaction, and
-- fk_attachments_storage_object is RESTRICT, so the storage object
-- cannot go while they remain. An attachment whose bytes never arrived
-- has nothing to show anyway.
DELETE FROM attachments
WHERE storage_object_id = ?;

-- name: DeleteCalendarEventAttachmentsForStorageObject :execrows
-- The calendar-side mirror of DeleteAttachmentsForStorageObject; the
-- same RESTRICT edge exists from calendar_event_attachments.
DELETE FROM calendar_event_attachments
WHERE storage_object_id = ?;

-- name: DeleteStorageObjectByID :execrows
-- Remove a reclaimed row. Restricted to rows nothing references so a
-- concurrent upload that adopted the row between listing and deleting
-- is left alone.
DELETE FROM storage_objects
WHERE id = sqlc.arg(id)
  AND ref_count = 0;
