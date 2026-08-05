/**
 * Redirect safety utilities shared across frontend apps.
 *
 * A redirect target is validated by resolving it against the origin the
 * app is served from and comparing the *resolved* origin against an
 * allowlist -- never by pattern-matching the raw string. Under the URL
 * spec a backslash is a path separator for http(s) URLs, so path-shaped
 * inputs such as `/\evil.com` resolve to a foreign origin and defeat any
 * "does it start with a single slash?" test. Resolving first cannot be
 * fooled that way, and it also covers the cross-origin sign-in hand-off
 * between the identity frontend and the product frontend, which run on
 * different hosts in a deployment.
 */

/** Schemes a navigation target may use. */
const SAFE_PROTOCOLS = new Set(['http:', 'https:']);

/**
 * normalizeOrigin returns the serialized origin of an absolute http(s)
 * URL, or null when the value is unparseable, uses another scheme, or
 * has an opaque origin (which serializes to the string "null" and must
 * never be treated as a match).
 */
function normalizeOrigin(value: string): string | null {
  try {
    const parsed = new URL(value);
    if (!SAFE_PROTOCOLS.has(parsed.protocol) || parsed.origin === 'null') return null;
    return parsed.origin;
  } catch {
    return null;
  }
}

/**
 * parseAllowedOrigins splits a comma-separated origin list -- typically
 * the `VITE_NF_ALLOWED_REDIRECT_ORIGINS` build-time env -- into
 * normalized origins ready for {@link isSafeRedirect}. Entries that are
 * not absolute http(s) URLs are dropped, so a typo in the deployment
 * config narrows the allowlist instead of widening it.
 */
export function parseAllowedOrigins(raw: string | undefined | null): string[] {
  if (!raw) return [];
  const origins: string[] = [];
  for (const entry of raw.split(',')) {
    const origin = normalizeOrigin(entry.trim());
    if (origin !== null && !origins.includes(origin)) origins.push(origin);
  }
  return origins;
}

/**
 * isSafeRedirect reports whether `url` may be used as a navigation
 * target. The value is resolved against `origin` (the origin the app is
 * served from, usually `window.location.origin`) and accepted only when
 * the resolved origin is `origin` itself or one of `allowedOrigins`.
 * Callers that only ever navigate within their own app omit
 * `allowedOrigins`; cross-app hand-offs pass the partner origins from
 * configuration.
 *
 * Backslashes are rejected up front, raw or percent-encoded: they are
 * path separators for http(s) URLs, so a value that survives one more
 * round of decoding downstream could otherwise change origin after it
 * was validated.
 */
export function isSafeRedirect(
  url: string,
  origin: string,
  allowedOrigins: readonly string[] = [],
): boolean {
  if (url.includes('\\') || url.toLowerCase().includes('%5c')) return false;

  const self = normalizeOrigin(origin);
  if (self === null) return false;

  let resolved: URL;
  try {
    resolved = new URL(url, self);
  } catch {
    return false;
  }
  if (!SAFE_PROTOCOLS.has(resolved.protocol)) return false;
  if (resolved.origin === self) return true;

  for (const allowed of allowedOrigins) {
    if (normalizeOrigin(allowed) === resolved.origin) return true;
  }
  return false;
}
