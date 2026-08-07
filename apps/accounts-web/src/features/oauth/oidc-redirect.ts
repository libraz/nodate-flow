/**
 * Carries `?redirect=` across the OIDC round trip.
 *
 * The provider hand-off leaves this app entirely: the browser goes to
 * `/auth/oidc/{provider}/start`, on to the IdP, back to the auth-api
 * callback, and finally lands on /oidc/complete with only a fragment.
 * Nothing the user typed survives that, and the start endpoint takes no
 * input, so the auth-api cannot fold the target into the OIDC `state`
 * either.
 *
 * The trip does stay in one browsing context on one origin, which is
 * exactly the scope of `sessionStorage` -- hence a short-lived entry
 * here rather than a server round trip. Storage is same-origin
 * script-writable, so the value is only a hint: {@link takeOidcRedirect}
 * re-validates it through {@link safeRedirectTarget}, the same check
 * /login applies to `?redirect=`, before anyone navigates to it.
 */

import { safeRedirectTarget } from '../../lib/redirect-target';

const STORAGE_KEY = 'nf.oidc.redirect';

/**
 * How long a stashed target stays usable. Long enough for a consent
 * screen plus the IdP's own MFA, short enough that an abandoned attempt
 * does not silently reroute an unrelated sign-in later in the same tab.
 */
const TTL_MS = 10 * 60 * 1000;

interface StoredRedirect {
  target: string;
  expiresAt: number;
}

/**
 * Remember where to land after the provider round trip. Unsafe or absent
 * targets clear any previous entry instead of being stored, so a stale
 * value can never outlive the attempt that set it.
 */
export function rememberOidcRedirect(target: string | null | undefined): void {
  const safe = safeRedirectTarget(target);
  try {
    if (safe === null) {
      window.sessionStorage.removeItem(STORAGE_KEY);
      return;
    }
    const stored: StoredRedirect = { target: safe, expiresAt: Date.now() + TTL_MS };
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(stored));
  } catch {
    // Storage can be unavailable (private mode, disabled cookies). The
    // sign-in itself still works; the user just lands on the default page.
  }
}

/**
 * Consume the remembered target, or null when there is none, it expired,
 * or it no longer passes the redirect check. Always clears the entry, so
 * a target is used at most once.
 */
export function takeOidcRedirect(): string | null {
  let raw: string | null = null;
  try {
    raw = window.sessionStorage.getItem(STORAGE_KEY);
    window.sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    return null;
  }
  if (raw === null) return null;

  let stored: StoredRedirect;
  try {
    stored = JSON.parse(raw) as StoredRedirect;
  } catch {
    return null;
  }
  if (typeof stored?.target !== 'string' || typeof stored?.expiresAt !== 'number') return null;
  if (Date.now() > stored.expiresAt) return null;
  return safeRedirectTarget(stored.target);
}
