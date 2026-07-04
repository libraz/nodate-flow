/**
 * SDK client for the accounts-web app (auth-api backend).
 *
 * Wires the auth store into the openapi-fetch client:
 *
 * 1. tokenProvider injects `Authorization: Bearer <token>` from the auth
 *    store on every request.
 * 2. A **request** middleware (createAuthRequestMiddleware) proactively
 *    refreshes the access token when it is within ~10s of expiry, so the
 *    outbound call always carries a fresh bearer and the browser never
 *    logs a 401 during normal navigation.
 * 3. A **response** middleware (createRefreshMiddleware) remains as a
 *    backstop: it catches unexpected 401s, attempts a single refresh
 *    against `POST /auth/refresh` (which uses the httpOnly nd_rt
 *    cookie), and replays the original request with the new token. On
 *    refresh failure the auth store is cleared and the original 401 is
 *    returned to the caller.
 *
 * The client uses `credentials: 'include'` so the refresh cookie is sent
 * automatically (configured inside `@nodate-flow/sdk`'s createClient).
 */

import {
  createAuthRequestMiddleware,
  createClient,
  createRefreshMiddleware,
  createTokenRefresher,
  type NodateFlowClient,
} from '@nodate-flow/sdk';

import { authStore } from '../features/auth/auth-store';

/** Base URL of the auth-api service (port 8082). */
export const authApiBaseUrl =
  (import.meta.env.VITE_AUTH_API_BASE_URL as string | undefined) ?? 'http://localhost:8082';

export const sdk: NodateFlowClient = createClient({
  baseUrl: authApiBaseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
});

/**
 * Memoized token refresh function with grace-window deduplication.
 * Exported so bootstrap hooks can trigger a proactive refresh.
 */
export const refreshAccessToken = createTokenRefresher({
  authApiBaseUrl,
  getAccessToken: () => authStore.getState().accessToken ?? undefined,
  setAccessToken: (token) => authStore.getState().setAccessToken(token),
  clearSession: () => authStore.getState().clearSession(),
});

// Proactive request-side middleware: awaits a refresh when the current
// access token is missing or within the expiry buffer. Registered FIRST
// so it runs before the response-side backstop and before any outbound
// call fires with a stale bearer.
const authRequestMiddleware = createAuthRequestMiddleware({
  getAccessToken: () => authStore.getState().accessToken ?? undefined,
  refresher: refreshAccessToken,
});
sdk.use(authRequestMiddleware);

// Reactive response-side middleware: retries once on an unexpected 401
// (e.g. server-side revocation). The proactive middleware above should
// make this path rare in normal operation.
const refreshMiddleware = createRefreshMiddleware(refreshAccessToken);
sdk.use(refreshMiddleware);
