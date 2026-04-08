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
INSERT INTO user_integrations (
  public_id,
  user_id,
  provider,
  external_account_id,
  external_account_label,
  scopes,
  access_token_ciphertext,
  refresh_token_ciphertext,
  access_token_expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  external_account_id = VALUES(external_account_id),
  external_account_label = VALUES(external_account_label),
  scopes = VALUES(scopes),
  access_token_ciphertext = VALUES(access_token_ciphertext),
  refresh_token_ciphertext = VALUES(refresh_token_ciphertext),
  access_token_expires_at = VALUES(access_token_expires_at),
  last_refreshed_at = CURRENT_TIMESTAMP,
  enabled = TRUE;

-- name: DeleteUserIntegration :exec
-- Hard-delete a single integration row (user-scoped).
DELETE FROM user_integrations
WHERE id = ?
  AND user_id = ?;

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
