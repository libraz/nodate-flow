/**
 * Token refresh utilities shared across all frontend apps.
 *
 * Provides two building blocks:
 *
 * 1. `createTokenRefresher` -- a memoized refresh function with
 *    grace-window deduplication so concurrent 401s collapse into a
 *    single refresh round-trip.
 *
 * 2. `createRefreshMiddleware` -- an openapi-fetch response middleware
 *    that intercepts 401 responses, attempts a token refresh, and
 *    replays the original request with the rotated token.
 */

/** Options for creating a token refresh middleware. */
export interface RefreshMiddlewareOptions {
  /** Base URL of the auth-api service (e.g. http://localhost:8082). */
  authApiBaseUrl: string;
  /** Returns the current access token. */
  getAccessToken: () => string | undefined;
  /** Stores a new access token after successful refresh. */
  setAccessToken: (token: string) => void;
  /** Clears the entire auth session on refresh failure. */
  clearSession: () => void;
  /**
   * Optional callback invoked after session is cleared on refresh
   * failure. Useful for redirecting to the login page.
   */
  onSessionExpired?: () => void;
}

/**
 * Grace window (ms) after a refresh resolves during which subsequent
 * 401s reuse the same resolved promise instead of triggering another
 * refresh. This prevents cascading refreshes that would invalidate the
 * single-use refresh cookie.
 */
const REFRESH_GRACE_MS = 1500;

/**
 * Creates a memoized `refreshAccessToken` function with grace-window
 * deduplication.
 *
 * - Concurrent callers share the same in-flight promise.
 * - After resolution the promise is held for {@link REFRESH_GRACE_MS}
 *   so that 401 responses from requests already in flight with the
 *   pre-rotation token reuse the same new token.
 * - On success: calls `setAccessToken` and returns the new token.
 * - On failure: calls `clearSession`, then `onSessionExpired` (if
 *   provided), and returns `null`.
 */
export function createTokenRefresher(
  options: RefreshMiddlewareOptions,
): () => Promise<string | null> {
  let refreshInFlight: Promise<string | null> | null = null;

  return function refreshAccessToken(): Promise<string | null> {
    if (refreshInFlight) return refreshInFlight;

    refreshInFlight = (async () => {
      try {
        const res = await fetch(`${options.authApiBaseUrl}/auth/refresh`, {
          method: 'POST',
          credentials: 'include',
        });
        if (!res.ok) {
          options.clearSession();
          options.onSessionExpired?.();
          return null;
        }
        const body = (await res.json()) as { accessToken?: string };
        const token = body.accessToken;
        if (!token) {
          options.clearSession();
          options.onSessionExpired?.();
          return null;
        }
        options.setAccessToken(token);
        return token;
      } catch {
        options.clearSession();
        options.onSessionExpired?.();
        return null;
      }
    })();

    // Hold the resolved promise for a grace window so concurrent 401s
    // that arrive within REFRESH_GRACE_MS all reuse the same token
    // instead of each triggering another refresh (which would
    // invalidate the single-use refresh cookie and cascade the session
    // into a logged-out state).
    const captured = refreshInFlight;
    void captured.finally(() => {
      setTimeout(() => {
        if (refreshInFlight === captured) refreshInFlight = null;
      }, REFRESH_GRACE_MS);
    });

    return refreshInFlight;
  };
}

/** Auth paths that must never trigger a refresh (prevents loops). */
const AUTH_SKIP_SUFFIXES = [
  '/auth/refresh',
  '/auth/login',
  '/auth/register',
  '/auth/logout',
] as const;

/**
 * Creates an openapi-fetch response middleware that intercepts 401
 * responses, attempts a token refresh via the provided `refreshFn`,
 * and replays the original request with the new token.
 *
 * Auth-related paths are excluded to prevent infinite loops.
 */
export function createRefreshMiddleware(refreshFn: () => Promise<string | null>): {
  onResponse: (ctx: { request: Request; response: Response }) => Promise<Response>;
} {
  return {
    async onResponse({ request, response }) {
      if (response.status !== 401) return response;

      const path = new URL(request.url).pathname;
      if (AUTH_SKIP_SUFFIXES.some((suffix) => path.endsWith(suffix))) {
        return response;
      }

      const newToken = await refreshFn();
      if (!newToken) return response;

      // Replay original request with the rotated token. Clone because
      // Request bodies are single-use streams.
      const replay = new Request(request, {
        headers: new Headers(request.headers),
      });
      replay.headers.set('Authorization', `Bearer ${newToken}`);
      return fetch(replay);
    },
  };
}
