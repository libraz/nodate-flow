/**
 * Maps backend error codes (RFC 7807 `type` URI suffix) to i18n keys
 * under `auth:errors.*`.
 */

import type { ProblemJson } from './api-client';

export type AuthErrorI18nKey =
  | 'auth:errors.invalidCredentials'
  | 'auth:errors.emailTaken'
  | 'auth:errors.accountLocked'
  | 'auth:errors.rateLimited'
  | 'auth:errors.passwordTooWeak'
  | 'auth:errors.registrationDisabled'
  | 'auth:errors.network'
  | 'auth:errors.totpCodeRequired'
  | 'auth:errors.totpCodeMismatch'
  | 'auth:errors.totpChallengeExpired'
  | 'auth:errors.totpRecoveryInvalid'
  | 'auth:errors.totpAlreadyEnrolled'
  | 'auth:errors.totpNotEnrolled'
  | 'auth:errors.passwordCurrentMismatch'
  | 'auth:errors.passwordNoLocalIdentity'
  | 'auth:errors.sessionExpired'
  | 'auth:errors.sessionRevoked'
  | 'auth:errors.tokenRefreshInvalid'
  | 'auth:errors.tokenRefreshExpired'
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
    const lastSegment = c.split('/').pop()?.split(':')[0]?.trim();
    if (lastSegment?.includes('.')) return lastSegment;
  }
  return null;
}

const AUTH_ERROR_MAP: Record<string, AuthErrorI18nKey> = {
  'AUTH.LOGIN.INVALID_CREDENTIALS': 'auth:errors.invalidCredentials',
  'AUTH.LOGIN.ACCOUNT_LOCKED': 'auth:errors.accountLocked',
  'AUTH.LOGIN.RATE_LIMITED_AFTER_RETRIES': 'auth:errors.rateLimited',
  'AUTH.REGISTER.EMAIL_ALREADY_TAKEN': 'auth:errors.emailTaken',
  'AUTH.REGISTER.PASSWORD_TOO_WEAK': 'auth:errors.passwordTooWeak',
  'AUTH.REGISTER.INSTANCE_REGISTRATION_DISABLED': 'auth:errors.registrationDisabled',
  'AUTH.TOTP.CODE_REQUIRED': 'auth:errors.totpCodeRequired',
  'AUTH.TOTP.CODE_MISMATCH': 'auth:errors.totpCodeMismatch',
  'AUTH.TOTP.CHALLENGE_EXPIRED': 'auth:errors.totpChallengeExpired',
  'AUTH.TOTP.RECOVERY_CODE_INVALID': 'auth:errors.totpRecoveryInvalid',
  'AUTH.TOTP.ALREADY_ENROLLED': 'auth:errors.totpAlreadyEnrolled',
  'AUTH.TOTP.NOT_ENROLLED': 'auth:errors.totpNotEnrolled',
  'AUTH.PASSWORD.CURRENT_MISMATCH': 'auth:errors.passwordCurrentMismatch',
  'AUTH.PASSWORD.TOO_WEAK': 'auth:errors.passwordTooWeak',
  'AUTH.PASSWORD.NO_LOCAL_IDENTITY': 'auth:errors.passwordNoLocalIdentity',
  'AUTH.SESSION.EXPIRED': 'auth:errors.sessionExpired',
  'AUTH.SESSION.REVOKED': 'auth:errors.sessionRevoked',
  'AUTH.TOKEN.REFRESH_INVALID': 'auth:errors.tokenRefreshInvalid',
  'AUTH.TOKEN.REFRESH_EXPIRED': 'auth:errors.tokenRefreshExpired',
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
