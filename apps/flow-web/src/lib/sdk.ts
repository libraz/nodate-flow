/**
 * Singleton SDK client for the web app.
 *
 * Wires the auth store into the openapi-fetch client:
 *
 * 1. tokenProvider injects `Authorization: Bearer <token>` from the auth
 *    store on every request.
 * 2. A response middleware catches 401 responses, attempts a single
 *    refresh against `POST /auth/refresh` (which uses the httpOnly nf_rt
 *    cookie), and replays the original request with the new token.
 *    On refresh failure the auth store is cleared and the original 401
 *    is returned to the caller.
 *
 * The client uses `credentials: 'include'` so the refresh cookie is sent
 * automatically (configured inside `@nodate-flow/sdk`'s createClient).
 */

import { type NodateFlowClient, createClient } from '@nodate-flow/sdk';

import { authStore } from '../features/auth/auth-store';

const baseUrl =
  (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? 'http://localhost:8080';

/** Resolved base URL for direct fetch fallbacks (refresh middleware). */
export const apiBaseUrl = baseUrl;

/** Base URL of the centralised auth-api service (token refresh, /me). */
export const authApiBaseUrl =
  (import.meta.env.VITE_AUTH_API_BASE_URL as string | undefined) ?? 'http://localhost:8082';

export const sdk: NodateFlowClient = createClient({
  baseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
});

/**
 * SDK client targeting the auth-api service (port 8082). Used for
 * workspace CRUD, member management, and invite endpoints which have
 * been consolidated into the Identity + Tenant Plane.
 */
export const authSdk: NodateFlowClient = createClient({
  baseUrl: authApiBaseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
});

/**
 * Calls POST /auth/refresh directly (bypassing the typed client to avoid
 * recursive middleware on 401). On success, updates the auth store and
 * returns the new access token. On failure, clears the session and
 * returns null.
 *
 * The in-flight promise is memoized so concurrent 401s collapse into a
 * single refresh round-trip. After a successful refresh, the resolved
 * promise is held for a short grace window (REFRESH_GRACE_MS) so that
 * 401 responses from requests that were already in flight with the
 * pre-rotation token all reuse the same new token instead of each
 * triggering another refresh — consecutive refreshes invalidate the
 * single-use refresh cookie and would cascade the whole session into
 * a logged-out state.
 */
const REFRESH_GRACE_MS = 1500;
let refreshInFlight: Promise<string | null> | null = null;

export function refreshAccessToken(): Promise<string | null> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      const res = await fetch(`${authApiBaseUrl}/auth/refresh`, {
        method: 'POST',
        credentials: 'include',
      });
      if (!res.ok) {
        authStore.getState().clearSession();
        return null;
      }
      const body = (await res.json()) as { accessToken?: string };
      const token = body.accessToken;
      if (!token) {
        authStore.getState().clearSession();
        return null;
      }
      authStore.getState().setAccessToken(token);
      return token;
    } catch {
      authStore.getState().clearSession();
      return null;
    }
  })();
  // Schedule the grace-window clear regardless of success/failure so a
  // single failed refresh still gates out subsequent attempts for a
  // moment (preventing thundering-herd refresh loops on logged-out
  // state), but eventually allows a fresh refresh attempt.
  const cleared = refreshInFlight;
  void cleared.finally(() => {
    setTimeout(() => {
      if (refreshInFlight === cleared) refreshInFlight = null;
    }, REFRESH_GRACE_MS);
  });
  return refreshInFlight;
}

// Response middleware: on 401, try a single refresh + replay. We avoid
// retrying refresh / login / logout themselves to prevent loops.
const refreshMiddleware = {
  async onResponse({ request, response }: { request: Request; response: Response }) {
    if (response.status !== 401) return response;
    const url = new URL(request.url);
    const path = url.pathname;
    if (
      path.endsWith('/auth/refresh') ||
      path.endsWith('/auth/login') ||
      path.endsWith('/auth/register') ||
      path.endsWith('/auth/logout')
    ) {
      return response;
    }
    const newToken = await refreshAccessToken();
    if (!newToken) return response;
    // Replay original request with the rotated token. We clone the
    // request because Request bodies are single-use streams.
    const replay = new Request(request, {
      headers: new Headers(request.headers),
    });
    replay.headers.set('Authorization', `Bearer ${newToken}`);
    return fetch(replay);
  },
};

sdk.use(refreshMiddleware);
authSdk.use(refreshMiddleware);
