-- name: RegisterUser :execlastid
-- Insert a new global user account. The caller supplies a UUID v7 public_id.
INSERT INTO users (
  public_id,
  email,
  display_name,
  locale,
  theme_preference
) VALUES (?, ?, ?, ?, ?);

-- name: FindUserByEmail :one
-- Lookup a user by email for login. Returns internal id for the auth pipeline.
SELECT
  id,
  public_id,
  email,
  email_verified_at,
  display_name,
  avatar_url,
  locale,
  theme_preference,
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
SELECT
  id,
  public_id,
  email,
  email_verified_at,
  display_name,
  avatar_url,
  locale,
  theme_preference,
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
  theme_preference
) VALUES (?, ?, ?, ?, ?);

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
-- Joins identities with users on email and provider='local'.
SELECT
  i.id,
  i.user_id,
  i.password_hash,
  i.failed_attempts,
  i.locked_until_at,
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
SELECT *
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

-- name: FindUserProfileById :one
-- Fetch the minimal profile for the /me endpoint by internal id.
SELECT public_id, email, display_name, locale, theme_preference, avatar_url,
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
    theme_preference               = COALESCE(sqlc.narg('theme_preference'), theme_preference),
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
