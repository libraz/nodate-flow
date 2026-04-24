/**
 * Singleton SDK client for the web app.
 *
 * Wires the auth store into the openapi-fetch client:
 *
 * 1. tokenProvider injects `Authorization: Bearer <token>` from the auth
 *    store on every request.
 * 2. A **request** middleware (createAuthRequestMiddleware) proactively
 *    refreshes the access token when it is within ~10s of expiry, so the
 *    outbound call always carries a fresh bearer. This prevents the
 *    well-known openapi-fetch console noise where a stale-token request
 *    elicits a 401 that the browser logs BEFORE the reactive response
 *    middleware can transparently replay it.
 * 3. A **response** middleware (createRefreshMiddleware) remains as a
 *    backstop: it catches unexpected 401s (e.g. a server-side token
 *    revocation), attempts a single refresh against `POST /auth/refresh`
 *    (which uses the httpOnly nf_rt cookie), and replays the original
 *    request with the new token. On refresh failure the auth store is
 *    cleared and the original 401 is returned to the caller.
 *
 * The client uses `credentials: 'include'` so the refresh cookie is sent
 * automatically (configured inside `@nodate-flow/sdk`'s createClient).
 */

import {
  type NodateFlowClient,
  createAuthRequestMiddleware,
  createClient,
  createRefreshMiddleware,
  createTokenRefresher,
} from '@nodate-flow/sdk';
import { type NodateTimeClient, createClient as createTimeClient } from '@nodate-flow/time-sdk';

import { authStore } from '../features/auth/auth-store';

const baseUrl =
  (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? 'http://localhost:8080';

/** Resolved base URL for direct fetch fallbacks (refresh middleware). */
export const apiBaseUrl = baseUrl;

/** Base URL of the centralised auth-api service (token refresh, /me). */
export const authApiBaseUrl =
  (import.meta.env.VITE_AUTH_API_BASE_URL as string | undefined) ?? 'http://localhost:8082';

/** Base URL of the time-api service (calendars / events). */
export const timeApiBaseUrl =
  (import.meta.env.VITE_TIME_API_BASE_URL as string | undefined) ?? 'http://localhost:8081';

export const sdk: NodateFlowClient = createClient({
  baseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
});

/**
 * SDK client targeting the time-api service (port 8081). Typed against
 * the generated time-api OpenAPI schema; used by the unified /calendar
 * page to read `/me/calendar-events` and per-workspace calendar CRUD.
 */
export const timeSdk: NodateTimeClient = createTimeClient({
  baseUrl: timeApiBaseUrl,
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
 * Memoized token refresh function with grace-window deduplication.
 * Exported so bootstrap hooks can trigger a proactive refresh.
 */
export const refreshAccessToken = createTokenRefresher({
  authApiBaseUrl,
  getAccessToken: () => authStore.getState().accessToken ?? undefined,
  setAccessToken: (token) => authStore.getState().setAccessToken(token),
  clearSession: () => authStore.getState().clearSession(),
});

const getAccessToken = (): string | undefined => authStore.getState().accessToken ?? undefined;

// Proactive request-side middleware: awaits a refresh when the current
// access token is missing or within the expiry buffer. Registered FIRST
// so it runs before the response-side backstop and before any outbound
// call fires with a stale bearer.
const authRequestMiddleware = createAuthRequestMiddleware({
  getAccessToken,
  refresher: refreshAccessToken,
});
sdk.use(authRequestMiddleware);
authSdk.use(authRequestMiddleware);
timeSdk.use(authRequestMiddleware);

// Reactive response-side middleware: retries once on an unexpected 401
// (e.g. server-side revocation). The proactive middleware above should
// make this path rare in normal operation.
const refreshMiddleware = createRefreshMiddleware(refreshAccessToken);
sdk.use(refreshMiddleware);
authSdk.use(refreshMiddleware);
timeSdk.use(refreshMiddleware);
