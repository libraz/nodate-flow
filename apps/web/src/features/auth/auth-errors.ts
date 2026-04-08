/**
 * Maps backend error codes (RFC7807 `type` URI suffix or matching
 * substring on `detail`/`title`) to i18n keys under `auth.errors.*`.
 *
 * The Huma backend serialises errors as application/problem+json with
 * the canonical code embedded in the `type` URI (e.g.
 * `https://nodate-flow.dev/errors/AUTH.LOGIN.INVALID_CREDENTIALS`).
 * We match by suffix to stay independent of the host portion.
 */

import { AuthErrors } from '@nodate-flow/sdk';

export type AuthErrorI18nKey =
  | 'auth.errors.invalid_credentials'
  | 'auth.errors.email_taken'
  | 'auth.errors.network'
  | 'auth.errors.totp_code_mismatch'
  | 'auth.errors.totp_challenge_expired'
  | 'auth.errors.totp_recovery_invalid'
  | 'auth.errors.generic'
  | 'auth.errors.unknown';

interface ProblemJson {
  type?: string;
  title?: string;
  detail?: string;
  status?: number;
}

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

/** Maps a problem+json payload to an i18n key. */
export function mapAuthError(problem: ProblemJson | null | undefined): AuthErrorI18nKey {
  const code = extractErrorCode(problem);
  if (!code) return 'auth.errors.unknown';
  if (code === AuthErrors.AUTH_LOGIN_INVALID_CREDENTIALS.code) {
    return 'auth.errors.invalid_credentials';
  }
  if (code === AuthErrors.AUTH_REGISTER_EMAIL_ALREADY_TAKEN.code) {
    return 'auth.errors.email_taken';
  }
  if (code === 'AUTH.TOTP.CODE_MISMATCH') {
    return 'auth.errors.totp_code_mismatch';
  }
  if (code === 'AUTH.TOTP.RECOVERY_CODE_INVALID') {
    return 'auth.errors.totp_recovery_invalid';
  }
  if (code === 'AUTH.SESSION.EXPIRED') {
    return 'auth.errors.totp_challenge_expired';
  }
  // TODO(f3): expand mapping for AUTH.LOGIN.ACCOUNT_LOCKED,
  // AUTH.LOGIN.RATE_LIMITED_AFTER_RETRIES, AUTH.REGISTER.PASSWORD_TOO_WEAK,
  // AUTH.REGISTER.INSTANCE_REGISTRATION_DISABLED once their i18n keys exist.
  return 'auth.errors.unknown';
}

/** Maps a thrown SDK error / network failure to an i18n key. */
export function mapAuthThrown(err: unknown): AuthErrorI18nKey {
  if (err instanceof TypeError) return 'auth.errors.network';
  return 'auth.errors.unknown';
}
