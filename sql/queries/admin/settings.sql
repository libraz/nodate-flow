-- name: AdminListInstanceSettings :many
-- List all instance settings.
SELECT
  public_id,
  setting_key,
  setting_value,
  enabled,
  created_at,
  updated_at
FROM instance_settings
WHERE enabled = TRUE
ORDER BY setting_key;

-- name: AdminGetInstanceSetting :one
-- Get a single setting by key.
SELECT
  public_id,
  setting_key,
  setting_value,
  enabled,
  created_at,
  updated_at
FROM instance_settings
WHERE setting_key = ? AND enabled = TRUE;

-- name: AdminUpsertInstanceSetting :exec
-- Insert or update an instance setting.
INSERT INTO instance_settings (public_id, setting_key, setting_value, updated_by_user_id)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  setting_value = VALUES(setting_value),
  updated_by_user_id = VALUES(updated_by_user_id);
