-- name: InsertRecoveryCode :exec
-- Insert a hashed recovery code for a user.
INSERT INTO user_recovery_codes (user_id, code_hash) VALUES (?, ?);

-- name: DeleteAllRecoveryCodesForUser :exec
-- Delete every recovery code (used or not) for a user.
DELETE FROM user_recovery_codes WHERE user_id = ?;

-- name: CountActiveRecoveryCodes :one
-- Count unused recovery codes for a user.
SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL;

-- name: FindUnusedRecoveryCode :one
-- Resolve an unused recovery code by (user_id, hash).
SELECT id FROM user_recovery_codes
WHERE user_id = ? AND code_hash = ? AND used_at IS NULL
LIMIT 1;

-- name: MarkRecoveryCodeUsed :exec
-- Stamp used_at on a recovery code by internal id.
UPDATE user_recovery_codes SET used_at = CURRENT_TIMESTAMP WHERE id = ?;
