-- name: FindIntegrationSourceMapping :one
-- Resolve the workspace an inbound webhook delivery belongs to from the
-- sender identity the provider put on the wire. Every /webhooks/*
-- receiver calls this before it writes anything; a miss means the
-- delivery has no tenant and must be rejected, never routed to a
-- default. Joined against workspaces so a mapping pointing at a
-- disabled tenant resolves to nothing.
SELECT
  m.id,
  m.public_id,
  m.workspace_id,
  m.label
FROM integration_source_mappings m
INNER JOIN workspaces w
  ON w.id = m.workspace_id
  AND w.enabled = TRUE
WHERE m.provider = ?
  AND m.external_key = ?
  AND m.enabled = TRUE
LIMIT 1;

-- name: ListIntegrationSourceMappings :many
-- List every mapping owned by a workspace, newest provider group first.
-- Disabled rows are included: they still hold the claim on the source,
-- so hiding them would make "already mapped" errors inexplicable.
SELECT
  public_id,
  provider,
  external_key,
  label,
  enabled,
  created_at,
  updated_at
FROM integration_source_mappings
WHERE workspace_id = ?
ORDER BY provider ASC, label ASC, id ASC;

-- name: FindIntegrationSourceMappingByPublicId :one
-- Load one mapping, workspace-scoped so a public id from another tenant
-- resolves to nothing.
SELECT
  public_id,
  provider,
  external_key,
  label,
  enabled,
  created_at,
  updated_at
FROM integration_source_mappings
WHERE workspace_id = ?
  AND public_id = ?
LIMIT 1;

-- name: CreateIntegrationSourceMapping :execlastid
-- Claim an external source for a workspace. The (provider, external_key)
-- UNIQUE key is instance-wide, so a second workspace claiming the same
-- source fails with a duplicate-entry error the handler turns into
-- INTEGRATION.MAPPING.SOURCE_ALREADY_MAPPED.
INSERT INTO integration_source_mappings (
  public_id,
  workspace_id,
  provider,
  external_key,
  label
) VALUES (?, ?, ?, ?, ?);

-- name: UpdateIntegrationSourceMapping :execrows
-- Patch the mutable fields of a mapping. NULL leaves a field unchanged.
-- provider and external_key are immutable: changing them would move the
-- claim to a different source, which is a create plus a delete.
UPDATE integration_source_mappings
SET
  label   = COALESCE(sqlc.narg('label'), label),
  enabled = COALESCE(sqlc.narg('enabled'), enabled)
WHERE workspace_id = sqlc.arg('workspace_id')
  AND public_id = sqlc.arg('public_id');

-- name: DeleteIntegrationSourceMapping :execrows
-- Release the claim on an external source. This is a hard delete, not
-- the usual enabled = FALSE soft delete: the (provider, external_key)
-- UNIQUE key is instance-wide, so a tombstone row would permanently
-- block the source from being mapped again by anyone, including its
-- rightful owner. Use enabled = FALSE to pause routing while keeping
-- the claim.
DELETE FROM integration_source_mappings
WHERE workspace_id = ?
  AND public_id = ?;
