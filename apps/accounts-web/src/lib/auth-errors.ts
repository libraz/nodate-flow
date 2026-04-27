/**
 * Maps backend error codes (RFC 7807 `type` URI suffix or matching
 * substring on `detail`/`title`) to i18n keys under `auth:errors.*`.
 *
 * The Huma backend serialises errors as application/problem+json with
 * the canonical code embedded in the `type` URI (e.g.
 * `https://nodate-flow.dev/errors/AUTH.LOGIN.INVALID_CREDENTIALS`).
 * We match by suffix to stay independent of the host portion.
 */

import { AuthErrors } from '@nodate-flow/sdk';

import type { ProblemJson } from './api-error';

export type AuthErrorI18nKey =
  | 'auth:errors.invalid_credentials'
  | 'auth:errors.email_taken'
  | 'auth:errors.account_locked'
  | 'auth:errors.rate_limited'
  | 'auth:errors.password_too_weak'
  | 'auth:errors.registration_disabled'
  | 'auth:errors.network'
  | 'auth:errors.totp_code_required'
  | 'auth:errors.totp_code_mismatch'
  | 'auth:errors.totp_challenge_expired'
  | 'auth:errors.totp_recovery_invalid'
  | 'auth:errors.totp_recovery_required'
  | 'auth:errors.totp_already_enrolled'
  | 'auth:errors.totp_not_enrolled'
  | 'auth:errors.totp_not_configured'
  | 'auth:errors.password_current_mismatch'
  | 'auth:errors.password_no_local_identity'
  | 'auth:errors.session_expired'
  | 'auth:errors.session_revoked'
  | 'auth:errors.oidc_state_mismatch'
  | 'auth:errors.oidc_nonce_mismatch'
  | 'auth:errors.oidc_id_token_invalid'
  | 'auth:errors.oidc_provider_unreachable'
  | 'auth:errors.oidc_github_not_configured'
  | 'auth:errors.oidc_microsoft_not_configured'
  | 'auth:errors.magic_link_malformed'
  | 'auth:errors.magic_link_expired'
  | 'auth:errors.magic_link_revoked'
  | 'auth:errors.magic_link_already_used'
  | 'auth:errors.magic_link_email_not_found'
  | 'auth:errors.pat_token_unknown'
  | 'auth:errors.pat_expired'
  | 'auth:errors.token_refresh_invalid'
  | 'auth:errors.token_refresh_expired'
  | 'auth:errors.token_signature_invalid'
  | 'auth:errors.generic'
  | 'auth:errors.unknown';

/**
 * Best-effort extraction of the canonical error code from a problem+json
 * payload. Returns the code (e.g. "AUTH.LOGIN.INVALID_CREDENTIALS") or
 * null if it cannot be determined.
 */
export function extractErrorCode(problem: ProblemJson | null | undefined): string | null {
  if (!problem) return null;
  const candidates: Array<string | undefined> = [problem.type, problem.detail, problem.title];
  for (const c of candidates) {
    if (!c) continue;
    // Match the last URI segment, e.g. ".../errors/AUTH.LOGIN.X" -> "AUTH.LOGIN.X".
    // Then strip any trailing human-readable suffix like "CODE: Something went wrong".
    const lastSegment = c.split('/').pop()?.split(':')[0]?.trim();
    if (lastSegment?.includes('.')) return lastSegment;
  }
  return null;
}

/**
 * Maps an error code to its corresponding i18n key. Covers all auth error
 * codes defined in errors/auth.yaml.
 */
const AUTH_ERROR_MAP: Record<string, AuthErrorI18nKey> = {
  // login
  [AuthErrors.AUTH_LOGIN_INVALID_CREDENTIALS.code]: 'auth:errors.invalid_credentials',
  [AuthErrors.AUTH_LOGIN_ACCOUNT_LOCKED.code]: 'auth:errors.account_locked',
  [AuthErrors.AUTH_LOGIN_RATE_LIMITED_AFTER_RETRIES.code]: 'auth:errors.rate_limited',
  // register
  [AuthErrors.AUTH_REGISTER_EMAIL_ALREADY_TAKEN.code]: 'auth:errors.email_taken',
  [AuthErrors.AUTH_REGISTER_PASSWORD_TOO_WEAK.code]: 'auth:errors.password_too_weak',
  [AuthErrors.AUTH_REGISTER_INSTANCE_REGISTRATION_DISABLED.code]:
    'auth:errors.registration_disabled',
  // totp
  [AuthErrors.AUTH_TOTP_CODE_REQUIRED.code]: 'auth:errors.totp_code_required',
  [AuthErrors.AUTH_TOTP_CODE_MISMATCH.code]: 'auth:errors.totp_code_mismatch',
  [AuthErrors.AUTH_TOTP_RECOVERY_CODE_INVALID.code]: 'auth:errors.totp_recovery_invalid',
  [AuthErrors.AUTH_TOTP_RECOVERY_CODE_REQUIRED.code]: 'auth:errors.totp_recovery_required',
  [AuthErrors.AUTH_TOTP_ALREADY_ENROLLED.code]: 'auth:errors.totp_already_enrolled',
  [AuthErrors.AUTH_TOTP_NOT_ENROLLED.code]: 'auth:errors.totp_not_enrolled',
  [AuthErrors.AUTH_TOTP_NOT_CONFIGURED.code]: 'auth:errors.totp_not_configured',
  // password
  [AuthErrors.AUTH_PASSWORD_CURRENT_MISMATCH.code]: 'auth:errors.password_current_mismatch',
  [AuthErrors.AUTH_PASSWORD_TOO_WEAK.code]: 'auth:errors.password_too_weak',
  [AuthErrors.AUTH_PASSWORD_NO_LOCAL_IDENTITY.code]: 'auth:errors.password_no_local_identity',
  // session
  [AuthErrors.AUTH_SESSION_EXPIRED.code]: 'auth:errors.session_expired',
  [AuthErrors.AUTH_SESSION_REVOKED.code]: 'auth:errors.session_revoked',
  // oidc
  [AuthErrors.AUTH_OIDC_STATE_MISMATCH.code]: 'auth:errors.oidc_state_mismatch',
  [AuthErrors.AUTH_OIDC_NONCE_MISMATCH.code]: 'auth:errors.oidc_nonce_mismatch',
  [AuthErrors.AUTH_OIDC_ID_TOKEN_INVALID.code]: 'auth:errors.oidc_id_token_invalid',
  [AuthErrors.AUTH_OIDC_PROVIDER_UNREACHABLE.code]: 'auth:errors.oidc_provider_unreachable',
  [AuthErrors.AUTH_OIDC_GITHUB_NOT_CONFIGURED.code]: 'auth:errors.oidc_github_not_configured',
  [AuthErrors.AUTH_OIDC_MICROSOFT_NOT_CONFIGURED.code]: 'auth:errors.oidc_microsoft_not_configured',
  // magic link
  [AuthErrors.AUTH_MAGIC_LINK_MALFORMED.code]: 'auth:errors.magic_link_malformed',
  [AuthErrors.AUTH_MAGIC_LINK_EXPIRED.code]: 'auth:errors.magic_link_expired',
  [AuthErrors.AUTH_MAGIC_LINK_REVOKED.code]: 'auth:errors.magic_link_revoked',
  [AuthErrors.AUTH_MAGIC_LINK_ALREADY_USED.code]: 'auth:errors.magic_link_already_used',
  [AuthErrors.AUTH_MAGIC_LINK_EMAIL_NOT_FOUND.code]: 'auth:errors.magic_link_email_not_found',
  // pat
  [AuthErrors.AUTH_PAT_TOKEN_UNKNOWN.code]: 'auth:errors.pat_token_unknown',
  [AuthErrors.AUTH_PAT_EXPIRED.code]: 'auth:errors.pat_expired',
  // token
  [AuthErrors.AUTH_TOKEN_REFRESH_INVALID.code]: 'auth:errors.token_refresh_invalid',
  [AuthErrors.AUTH_TOKEN_REFRESH_EXPIRED.code]: 'auth:errors.token_refresh_expired',
  [AuthErrors.AUTH_TOKEN_SIGNATURE_INVALID.code]: 'auth:errors.token_signature_invalid',
};

/** Maps a problem+json payload to an i18n key. */
export function mapAuthError(problem: ProblemJson | null | undefined): AuthErrorI18nKey {
  const code = extractErrorCode(problem);
  if (!code) return 'auth:errors.unknown';
  return AUTH_ERROR_MAP[code] ?? 'auth:errors.unknown';
}

/** Maps a thrown SDK error / network failure to an i18n key. */
export function mapAuthThrown(err: unknown): AuthErrorI18nKey {
  if (err instanceof TypeError) return 'auth:errors.network';
  return 'auth:errors.unknown';
}
