-- name: ListUserIntegrations :many
-- List every active integration owned by a user. Tokens are NOT
-- selected; only metadata used by the /me/integrations list view.
SELECT
  public_id,
  provider,
  external_account_id,
  external_account_label,
  scopes,
  access_token_expires_at,
  connected_at,
  last_refreshed_at
FROM user_integrations
WHERE user_id = ?
  AND enabled = TRUE
ORDER BY connected_at DESC, id DESC;

-- name: FindUserIntegrationByPublicId :one
-- Resolve a single integration by its public id, user-scoped. Used
-- by DELETE /me/integrations/{publicId}.
SELECT
  id,
  public_id,
  user_id,
  provider,
  external_account_label
FROM user_integrations
WHERE public_id = ?
  AND user_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindUserIntegrationByUserProvider :one
-- Resolve a user's integration for a specific provider. Returns the
-- encrypted tokens so the caller can refresh or use them.
SELECT
  id,
  public_id,
  access_token_ciphertext,
  refresh_token_ciphertext,
  access_token_expires_at
FROM user_integrations
WHERE user_id = ?
  AND provider = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpsertUserIntegration :execlastid
-- Insert or replace a user+provider integration. The uniq
-- (user_id, provider) key guarantees only one active row per
-- provider per user; on conflict we refresh every token column.
-- metadata_json carries provider-specific binding metadata (e.g.
-- {"external_user_id": "<snowflake>", "verified_at": "..."} for
-- provider='discord'); pass NULL for providers that do not set it.
INSERT INTO user_integrations (
  public_id,
  user_id,
  provider,
  external_account_id,
  external_account_label,
  scopes,
  access_token_ciphertext,
  refresh_token_ciphertext,
  access_token_expires_at,
  metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  external_account_id = VALUES(external_account_id),
  external_account_label = VALUES(external_account_label),
  scopes = VALUES(scopes),
  access_token_ciphertext = VALUES(access_token_ciphertext),
  refresh_token_ciphertext = VALUES(refresh_token_ciphertext),
  access_token_expires_at = VALUES(access_token_expires_at),
  metadata_json = VALUES(metadata_json),
  last_refreshed_at = CURRENT_TIMESTAMP,
  enabled = TRUE;

-- name: DeleteUserIntegration :exec
-- Hard-delete a single integration row (user-scoped).
DELETE FROM user_integrations
WHERE id = ?
  AND user_id = ?;

-- name: ListConnectionsExpiringBefore :many
-- List enabled integrations whose access token will expire before
-- the given cutoff AND still have a stored refresh token. Used by
-- the background token refresher.
SELECT
  id,
  user_id,
  provider,
  access_token_ciphertext,
  refresh_token_ciphertext,
  access_token_expires_at
FROM user_integrations
WHERE enabled = TRUE
  AND access_token_expires_at IS NOT NULL
  AND access_token_expires_at < ?
  AND refresh_token_ciphertext IS NOT NULL
  AND LENGTH(refresh_token_ciphertext) > 0
ORDER BY access_token_expires_at ASC
LIMIT 200;

-- name: UpdateConnectionTokens :exec
-- Replace stored tokens after a successful refresh.
UPDATE user_integrations
SET access_token_ciphertext = ?,
    refresh_token_ciphertext = ?,
    access_token_expires_at = ?,
    last_refreshed_at = NOW(),
    updated_at = NOW()
WHERE id = ?;

-- name: CreateOauthState :exec
-- Insert a short-lived CSRF state row for the personal OAuth flow.
INSERT INTO oauth_states (
  state,
  user_id,
  provider,
  redirect_to,
  expires_at
) VALUES (?, ?, ?, ?, ?);

-- name: ConsumeOauthState :one
-- Atomically look up and delete an OAuth state row. The caller MUST
-- still check expires_at against CURRENT_TIMESTAMP before trusting
-- the returned row.
SELECT
  state,
  user_id,
  provider,
  redirect_to,
  expires_at
FROM oauth_states
WHERE state = ?
LIMIT 1;

-- name: DeleteOauthState :exec
-- Explicit delete for the state row that :one above just returned.
DELETE FROM oauth_states
WHERE state = ?;

-- name: PurgeExpiredOauthStates :exec
-- Garbage-collect oauth_states rows past their expires_at. Called
-- opportunistically from the callback handler.
DELETE FROM oauth_states
WHERE expires_at < CURRENT_TIMESTAMP;

-- name: FindUserByDiscordSnowflake :one
-- Resolve a Discord user snowflake to (user_public_id, workspace_public_id)
-- via the user_integrations.metadata_json $.external_user_id binding.
-- Used by the internal /internal/users/by-discord/{snowflake} lookup
-- endpoint the presence-discord gateway calls before emitting a signal.
-- Filters out soft-disabled integrations and deterministically returns
-- the user's earliest-joined workspace as the "default" since there is
-- no users.default_workspace_id column today (v1.0 limitation; future
-- enhancement may add an explicit default-workspace column). The tiebreak
-- on wm.id keeps the result stable when two memberships share a created_at.
-- JSON_UNQUOTE(JSON_EXTRACT(...)) is used instead of comparing JSON
-- values directly: MySQL JSON equality on string scalars returns 0 even
-- when the printed values are identical (MySQL JSON values carry an
-- internal type tag that the JSON_QUOTE round-trip does not produce
-- identically). Unquoting the stored value and comparing against the
-- raw string parameter avoids that quirk.
SELECT
  u.public_id AS user_public_id,
  w.public_id AS workspace_public_id
FROM user_integrations ui
INNER JOIN users u ON u.id = ui.user_id AND u.enabled = TRUE
INNER JOIN workspace_members wm ON wm.user_id = u.id AND wm.enabled = TRUE
INNER JOIN workspaces w ON w.id = wm.workspace_id AND w.enabled = TRUE
WHERE ui.provider = 'discord'
  AND ui.enabled = TRUE
  AND JSON_UNQUOTE(JSON_EXTRACT(ui.metadata_json, '$.external_user_id')) = CAST(sqlc.arg(snowflake) AS CHAR)
ORDER BY wm.created_at ASC, wm.id ASC
LIMIT 1;
