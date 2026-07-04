/**
 * Maps backend error codes (RFC 7807 `type` value or matching substring
 * on `detail`/`title`) to i18n keys.
 *
 * Resolution is catalog-driven:
 *   1. RFC 9457 `extensions.x-i18n-key` / `extensions.i18nKey`.
 *   2. Generated SDK error catalog `i18nKey`.
 *   3. Generated `errors:${code}` locale entry.
 */

import { ApiError, lookupErrorDefinition, lookupErrorI18nKey } from '@nodate-flow/sdk';

import type { ProblemJson } from './api-error';

export type AuthErrorI18nKey = string;

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

function normalizeI18nKey(key: string | undefined): AuthErrorI18nKey | null {
  if (!key) return null;
  if (key.includes(':')) return key;
  const [ns, ...rest] = key.split('.');
  if (!ns || rest.length === 0) return key;
  return `${ns}:${rest.join('.')}`;
}

function extensionI18nKey(problem: ProblemJson | null | undefined): string | undefined {
  const ext = problem?.extensions;
  if (!ext) return undefined;
  const key = ext['x-i18n-key'] ?? ext.i18nKey;
  return typeof key === 'string' ? key : undefined;
}

/** Maps a problem+json payload to an i18n key. */
export function mapAuthError(problem: ProblemJson | null | undefined): AuthErrorI18nKey {
  const extensionKey = normalizeI18nKey(extensionI18nKey(problem));
  if (extensionKey) return extensionKey;

  const code = extractErrorCode(problem);
  if (!code) return 'auth:errors.unknown';

  const catalogKey = normalizeI18nKey(lookupErrorI18nKey(code));
  if (catalogKey) return catalogKey;

  if (lookupErrorDefinition(code)) return `errors:${code}`;
  return 'auth:errors.unknown';
}

/** Maps a thrown SDK error / network failure to an i18n key. */
export function mapAuthThrown(err: unknown): AuthErrorI18nKey {
  if (err instanceof TypeError) return 'auth:errors.network';
  if (err instanceof ApiError) {
    const directKey = normalizeI18nKey(err.i18nKey);
    if (directKey) return directKey;
    if (err.code && lookupErrorDefinition(err.code)) return `errors:${err.code}`;
  }
  return 'auth:errors.unknown';
}
