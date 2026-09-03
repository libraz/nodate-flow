-- name: InsertRecoveryCode :exec
-- Insert a hashed recovery code for a user.
INSERT INTO user_recovery_codes (user_id, code_hash) VALUES (?, ?);

-- name: DeleteAllRecoveryCodesForUser :exec
-- Delete every recovery code (used or not) for a user.
--
-- affected-rows: not-applicable — it empties the whole set before a fresh
-- batch is issued, and on the disable path it is the emptiness that is
-- wanted. A user who holds no codes is already where this leaves them.
DELETE FROM user_recovery_codes WHERE user_id = ?;

-- name: CountActiveRecoveryCodes :one
-- Count unused recovery codes for a user.
SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL;

-- name: FindUnusedRecoveryCode :one
-- Resolve an unused recovery code by (user_id, hash).
SELECT id FROM user_recovery_codes
WHERE user_id = ? AND code_hash = ? AND used_at IS NULL
LIMIT 1;

-- name: MarkRecoveryCodeUsed :execrows
-- Atomically claim a recovery code by internal id. The WHERE clause
-- includes used_at IS NULL so two concurrent login requests racing on
-- the same code can never both succeed: exactly one UPDATE will match
-- and the loser sees zero affected rows. Callers MUST inspect
-- RowsAffected and treat 0 as "already consumed" (reject the attempt).
UPDATE user_recovery_codes
SET used_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND used_at IS NULL;
