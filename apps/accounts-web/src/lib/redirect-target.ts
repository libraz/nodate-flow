/**
 * Where sign-in is allowed to send the user afterwards.
 *
 * Every post-authentication navigation target -- `?redirect=` on /login,
 * the value carried across an OIDC round trip -- resolves through this
 * one module so there is a single answer to "is this target safe?".
 * The check itself lives in the SDK ({@link isSafeRedirect}), which
 * resolves the value against this app's origin instead of pattern
 * matching the raw string.
 */

import { isSafeRedirect, parseAllowedOrigins } from '@nodate-flow/sdk';

/**
 * Origins other than this app's own that a redirect may point at.
 * Sign-in hands the user back to the product frontend, which runs on a
 * different origin in every deployment (and on a different port in local
 * dev), so the set has to come from configuration:
 * `VITE_NF_ALLOWED_REDIRECT_ORIGINS` (comma-separated) plus the product
 * frontend origin the profile page already links to. Anything else is
 * refused and the user lands on /profile instead.
 */
export const ALLOWED_REDIRECT_ORIGINS = parseAllowedOrigins(
  [
    import.meta.env.VITE_NF_ALLOWED_REDIRECT_ORIGINS ?? '',
    import.meta.env.VITE_FLOW_WEB_URL ?? '',
    // Local dev serves the two apps on different ports of localhost, so
    // the hand-off is cross-origin there too. Dev-only: a production
    // build must name its product origin in the config above.
    import.meta.env.DEV ? 'http://localhost:5173' : '',
  ].join(','),
);

/**
 * Returns `target` when it is safe to navigate to, otherwise null so the
 * caller falls back to its own default landing page.
 */
export function safeRedirectTarget(target: string | null | undefined): string | null {
  if (!target) return null;
  if (typeof window === 'undefined') return null;
  return isSafeRedirect(target, window.location.origin, ALLOWED_REDIRECT_ORIGINS) ? target : null;
}
