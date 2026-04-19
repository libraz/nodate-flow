/**
 * Redirect safety utilities shared across frontend apps.
 *
 * Prevents open-redirect attacks by validating that redirect targets
 * are either relative paths (starting with a single slash) or point
 * to the same origin as the current page.
 */

/**
 * isSafeRedirect returns true when the URL is a relative path (starting
 * with a single slash) or points to the same origin as the current page.
 * Protocol-relative URLs ("//evil.com") and foreign origins are rejected
 * to prevent open-redirect attacks via the ?redirect= query parameter.
 */
export function isSafeRedirect(url: string): boolean {
  // Relative path (but not protocol-relative "//...")
  if (url.startsWith('/') && !url.startsWith('//')) return true;
  try {
    const parsed = new URL(url);
    return parsed.origin === window.location.origin;
  } catch {
    return false;
  }
}
