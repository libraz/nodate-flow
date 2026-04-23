/**
 * SDK clients for the time-web app.
 *
 * Two client instances:
 * - `sdk` targets time-api (calendar/event endpoints)
 * - `authSdk` targets auth-api (login, refresh, /me)
 *
 * Both wire the auth store into the openapi-fetch client via tokenProvider
 * and install two middlewares:
 *
 * 1. A **request** middleware (createAuthRequestMiddleware) proactively
 *    refreshes the access token when it is within ~10s of expiry, so the
 *    outbound call always carries a fresh bearer and the browser never
 *    logs a 401 during normal navigation.
 * 2. A **response** middleware (createRefreshMiddleware) remains as a
 *    backstop that retries once against `POST /auth/refresh` on an
 *    unexpected 401 and replays the original request with the new token.
 */

import {
  createAuthRequestMiddleware,
  createClient,
  createRefreshMiddleware,
  createTokenRefresher,
  isSafeRedirect,
} from '@nodate-flow/sdk';

import { authStore } from '../features/auth/auth-store';

/**
 * Untyped SDK helper. The time-api calendar endpoints and auth-api
 * endpoints are not fully in the shared OpenAPI spec, so we expose
 * loose method signatures. Callers cast responses locally.
 */
interface UntypedResponse {
  data?: unknown;
  error?: unknown;
  response: Response;
}

type AnySdk = {
  // biome-ignore lint/suspicious/noExplicitAny: time/auth-api paths not in OpenAPI spec
  [K in 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE']: (...args: any[]) => Promise<UntypedResponse>;
} & { use: (middleware: unknown) => void };

/** Base URL of the time-api service (port 8081). */
export const apiBaseUrl =
  (import.meta.env.VITE_API_URL as string | undefined) ?? 'http://localhost:8081';

/** Base URL of the auth-api service (port 8082). */
export const authApiBaseUrl =
  (import.meta.env.VITE_AUTH_API_BASE_URL as string | undefined) ?? 'http://localhost:8082';

/** Base URL of the accounts-web frontend. */
export const accountsWebUrl =
  (import.meta.env.VITE_ACCOUNTS_WEB_URL as string | undefined) ?? 'http://localhost:5175';

/** Base URL of the flow-web frontend (owns the unified /calendar UX). */
export const flowWebUrl =
  (import.meta.env.VITE_FLOW_WEB_URL as string | undefined) ?? 'http://localhost:5173';

/** SDK client targeting the time-api service. */
export const sdk = createClient({
  baseUrl: apiBaseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
}) as unknown as AnySdk;

/** SDK client targeting the auth-api service. */
export const authSdk = createClient({
  baseUrl: authApiBaseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
}) as unknown as AnySdk;

/**
 * Memoized token refresh function with grace-window deduplication.
 * On failure, redirects to the accounts-web login page.
 */
export const refreshAccessToken = createTokenRefresher({
  authApiBaseUrl,
  getAccessToken: () => authStore.getState().accessToken ?? undefined,
  setAccessToken: (token) => authStore.getState().setAccessToken(token),
  clearSession: () => authStore.getState().clearSession(),
  onSessionExpired: () => {
    const target = `${accountsWebUrl}/login?redirect=${encodeURIComponent(window.location.href)}`;
    if (isSafeRedirect(target)) {
      window.location.href = target;
    }
  },
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
authSdk.use(authRequestMiddleware);

// Reactive response-side middleware: retries once on an unexpected 401
// (e.g. server-side revocation). The proactive middleware above should
// make this path rare in normal operation.
const refreshMiddleware = createRefreshMiddleware(refreshAccessToken);
sdk.use(refreshMiddleware);
authSdk.use(refreshMiddleware);
