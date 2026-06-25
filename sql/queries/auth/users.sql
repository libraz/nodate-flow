-- name: RegisterUser :execlastid
-- Insert a new global user account. The caller supplies a UUID v7 public_id.
INSERT INTO users (
  public_id,
  email,
  display_name,
  locale,
  timezone,
  country,
  theme_preference
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: FindUserByEmail :one
-- Lookup a user by email for login. Returns internal id for the auth pipeline.
-- avatar_storage_object_id is the FK for self-hosted avatars; avatar_url is
-- the external (e.g. OIDC provider) fallback URL.
SELECT
  id,
  public_id,
  email,
  email_verified_at,
  display_name,
  avatar_url,
  avatar_storage_object_id,
  locale,
  timezone,
  country,
  theme_preference,
  calendar_shift_default,
  last_login_at,
  enabled,
  updated_at,
  created_at
FROM users
WHERE email = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindUserByEmailIncludingDisabled :one
-- Lookup a user by email regardless of enabled flag (for invitation reuse).
-- avatar_storage_object_id is the FK for self-hosted avatars; avatar_url is
-- the external (e.g. OIDC provider) fallback URL.
SELECT
  id,
  public_id,
  email,
  email_verified_at,
  display_name,
  avatar_url,
  avatar_storage_object_id,
  locale,
  timezone,
  country,
  theme_preference,
  calendar_shift_default,
  last_login_at,
  enabled,
  updated_at,
  created_at
FROM users
WHERE email = ?
LIMIT 1;

-- name: CreateStubUser :execlastid
-- Insert a stub user for invitations: no identity row, display_name defaults to email.
INSERT INTO users (
  public_id,
  email,
  display_name,
  locale,
  timezone,
  country,
  theme_preference
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: CreateIdentity :execlastid
-- Insert a new identity row (local password or OIDC binding) for a user.
INSERT INTO identities (
  public_id,
  user_id,
  provider,
  subject,
  password_hash
) VALUES (?, ?, ?, ?, ?);

-- name: FindLocalIdentityByEmail :one
-- Resolve a local-password identity by user email for the login pipeline.
-- Joins identities with users on email and provider='local'. Also
-- returns the TOTP columns so the login handler can decide whether
-- to issue session tokens directly or return a totp-challenge.
SELECT
  i.id,
  i.user_id,
  i.password_hash,
  i.failed_attempts,
  i.locked_until_at,
  i.mfa_secret_ciphertext,
  i.mfa_confirmed_at,
  i.mfa_last_step,
  u.public_id AS user_public_id,
  u.enabled AS user_enabled
FROM identities i
INNER JOIN users u ON u.id = i.user_id
WHERE u.email = ?
  AND i.provider = 'local'
  AND i.enabled = TRUE
  AND u.enabled = TRUE
LIMIT 1;

-- name: FindUserByPublicId :one
-- Lookup a user by external public_id (UUID v7) via the v_users view.
SELECT
  workspace_id,
  public_id,
  email,
  display_name,
  avatar_url,
  locale,
  timezone,
  country,
  week_start,
  theme_preference,
  calendar_shift_default,
  workspace_role,
  last_login_at,
  updated_at,
  created_at
FROM v_users
WHERE public_id = ?
LIMIT 1;

-- name: FindIdentityByProviderSubject :one
-- Resolve an identity by (provider, subject) pair for OIDC login flows.
SELECT
  id,
  user_id
FROM identities
WHERE provider = ?
  AND subject = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindLocalIdentityByUserId :one
-- Resolve a local-password identity by internal user id. Used by
-- /me/password to verify the caller's current password, by the
-- TOTP handlers to read / write mfa_secret_ciphertext, and by
-- LoginTotp to enforce brute-force lockout on 2FA attempts.
SELECT
  id,
  password_hash,
  failed_attempts,
  locked_until_at,
  mfa_secret_ciphertext,
  mfa_confirmed_at,
  mfa_last_step
FROM identities
WHERE user_id = ?
  AND provider = 'local'
  AND enabled = TRUE
LIMIT 1;

-- name: SetIdentityMfaSecret :exec
-- Begin (or restart) TOTP enrollment by writing a fresh encrypted
-- secret and clearing any previous confirmation timestamp.
UPDATE identities
SET mfa_secret_ciphertext = ?,
    mfa_confirmed_at = NULL,
    mfa_last_step = NULL
WHERE id = ?;

-- name: ConfirmIdentityMfa :exec
-- Mark a pending TOTP enrollment as confirmed by stamping
-- mfa_confirmed_at. The caller must have already validated a code
-- against the stored secret.
UPDATE identities
SET mfa_confirmed_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ClearIdentityMfa :exec
-- Disable TOTP on a local identity.
UPDATE identities
SET mfa_secret_ciphertext = NULL,
    mfa_confirmed_at = NULL,
    mfa_last_step = NULL
WHERE id = ?;

-- name: UpdateIdentityMfaLastStep :exec
-- Record the highest TOTP time-step that has been accepted for this
-- identity. RFC 6238 5.2 one-time-use: a subsequent code whose step is
-- <= this value must be rejected as a replay. The guard never lowers
-- the stored value so an out-of-order skew-window match cannot reopen
-- an already-consumed step.
UPDATE identities
SET mfa_last_step = sqlc.arg(step)
WHERE id = sqlc.arg(id)
  AND (mfa_last_step IS NULL OR mfa_last_step < sqlc.arg(step));

-- name: UpdateIdentityPasswordHash :exec
-- Replace the Argon2id password hash on a local identity.
UPDATE identities
SET password_hash = ?
WHERE id = ?;

-- name: UpdateIdentityFailedAttempts :exec
-- Bump failed login counter and optionally apply a lockout deadline.
UPDATE identities
SET failed_attempts = ?,
    locked_until_at = ?
WHERE id = ?;

-- name: ResetIdentityFailedAttempts :exec
-- Clear failed login counter and lockout after a successful authentication.
UPDATE identities
SET failed_attempts = 0,
    locked_until_at = NULL
WHERE id = ?;

-- name: FindUserInternalIdByPublicId :one
-- Resolve the internal users.id for a public UUID, excluding disabled rows.
SELECT id
FROM users
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindUserPublicIdById :one
-- Resolve the public UUID for an internal users.id, excluding disabled rows.
SELECT public_id
FROM users
WHERE id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindUserProfileById :one
-- Fetch the minimal profile for the /me endpoint by internal id.
-- avatar_storage_object_id is the FK for a self-hosted (uploaded) avatar;
-- avatar_url stays for externally hosted avatars (e.g. OIDC provider). The
-- handler resolves the storage object public_id / signed URL separately.
SELECT public_id, email, display_name, locale, timezone, country, week_start, theme_preference, calendar_shift_default,
       avatar_url, avatar_storage_object_id,
       notif_email_digest_enabled, notif_email_mention_enabled,
       notif_email_assignment_enabled, notif_email_due_soon_enabled,
       notif_web_push_enabled
FROM users
WHERE id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: PatchMe :exec
-- Patch the authenticated user's profile. NULL params leave the column untouched.
UPDATE users
SET display_name                   = COALESCE(sqlc.narg('display_name'), display_name),
    locale                         = COALESCE(sqlc.narg('locale'), locale),
    timezone                       = COALESCE(sqlc.narg('timezone'), timezone),
    country                        = COALESCE(sqlc.narg('country'), country),
    week_start                     = COALESCE(sqlc.narg('week_start'), week_start),
    theme_preference               = COALESCE(sqlc.narg('theme_preference'), theme_preference),
    calendar_shift_default         = COALESCE(sqlc.narg('calendar_shift_default'), calendar_shift_default),
    avatar_url                     = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    notif_email_digest_enabled     = COALESCE(sqlc.narg('notif_email_digest_enabled'), notif_email_digest_enabled),
    notif_email_mention_enabled    = COALESCE(sqlc.narg('notif_email_mention_enabled'), notif_email_mention_enabled),
    notif_email_assignment_enabled = COALESCE(sqlc.narg('notif_email_assignment_enabled'), notif_email_assignment_enabled),
    notif_email_due_soon_enabled   = COALESCE(sqlc.narg('notif_email_due_soon_enabled'), notif_email_due_soon_enabled),
    notif_web_push_enabled         = COALESCE(sqlc.narg('notif_web_push_enabled'), notif_web_push_enabled)
WHERE id = ?
  AND enabled = TRUE;

-- name: UpdateUserLastLoginAt :exec
-- Stamp last successful login time on a user account.
UPDATE users
SET last_login_at = NOW()
WHERE id = ?;

-- name: SetMyAvatarURL :exec
-- Replace the authenticated user's avatar_url column with a non-NULL value.
-- Used by POST /me/avatar after a successful upload. The COALESCE-style
-- PatchMe query cannot be reused because it treats NULL as "leave alone"
-- rather than "overwrite with this value".
UPDATE users
SET avatar_url = ?
WHERE id = ?
  AND enabled = TRUE;

-- name: ClearMyAvatarURL :exec
-- Null out the authenticated user's avatar_url column. Used by
-- DELETE /me/avatar after the object has been removed from storage.
-- PatchMe cannot be used because NULL narg means "leave alone" there.
UPDATE users
SET avatar_url = NULL
WHERE id = ?
  AND enabled = TRUE;

-- name: SetMyAvatarStorageObject :exec
-- Bind the authenticated user's avatar to a freshly inserted (or dedup-hit)
-- storage_objects row. Used by POST /me/avatar after a successful upload to
-- MinIO. Caller MUST run this in the same transaction as the InsertStorageObject
-- / IncrementStorageObjectRefCount that allocated the FK target so ref_count
-- never drifts. PatchMe cannot be used because NULL narg means "leave alone".
UPDATE users
SET avatar_storage_object_id = ?
WHERE id = ?
  AND enabled = TRUE;

-- name: ClearMyAvatarStorageObject :exec
-- Null out the authenticated user's avatar_storage_object_id column. Used by
-- DELETE /me/avatar; caller MUST decrement ref_count on the previously linked
-- storage_objects row (and possibly DeleteStorageObjectIfUnreferenced) inside
-- the same transaction. PatchMe cannot be used because NULL narg means
-- "leave alone".
UPDATE users
SET avatar_storage_object_id = NULL
WHERE id = ?
  AND enabled = TRUE;
