/**
 * @brief Resolve the public-facing origin used to build outbound share /
 * invite URLs.
 *
 * @return Base URL string without a trailing slash, suitable for
 * concatenating with a path (e.g. `${base}/share/cal/${token}`).
 *
 * Resolution order:
 *   1. `VITE_PUBLIC_BASE_URL` build-time env (Vite-prefixed). Use this in
 *      production / preview deployments where the runtime origin differs
 *      from the user-facing canonical origin (reverse proxies, custom
 *      domains).
 *   2. `window.location.origin` as a runtime fallback. In development
 *      mode this also emits a one-shot console warning so the missing
 *      env is visible during local QA.
 *   3. Empty string for non-browser contexts (SSR / unit tests). Callers
 *      should treat the empty string as "no usable origin" and avoid
 *      composing partial URLs.
 *
 * The trailing slash is always stripped to keep concatenation
 * deterministic regardless of how the env is configured.
 */

let warned = false;

export function getPublicBaseUrl(): string {
  const fromEnv = (import.meta.env.VITE_PUBLIC_BASE_URL as string | undefined) ?? '';
  if (fromEnv !== '') {
    return stripTrailingSlash(fromEnv);
  }

  if (typeof window !== 'undefined' && typeof window.location !== 'undefined') {
    if (import.meta.env.DEV && !warned) {
      warned = true;
      // eslint-disable-next-line no-console
      console.warn(
        '[flow-web] VITE_PUBLIC_BASE_URL is not set; falling back to window.location.origin. ' +
          'Set VITE_PUBLIC_BASE_URL in apps/flow-web/.env to the canonical user-facing origin.',
      );
    }
    return stripTrailingSlash(window.location.origin);
  }

  return '';
}

function stripTrailingSlash(value: string): string {
  return value.endsWith('/') ? value.slice(0, -1) : value;
}
