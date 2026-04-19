/**
 * SDK clients for the time-web app.
 *
 * Two client instances:
 * - `sdk` targets time-api (calendar/event endpoints)
 * - `authSdk` targets auth-api (login, refresh, /me)
 *
 * Both wire the auth store into the openapi-fetch client via tokenProvider.
 * A response middleware catches 401 responses, attempts a single refresh
 * against auth-api's `POST /auth/refresh`, and replays the original
 * request with the new token.
 */

import { createClient, createRefreshMiddleware, createTokenRefresher } from '@nodate-flow/sdk';

/**
 * isSafeRedirect returns true when the URL is a relative path (starting
 * with a single slash) or points to the same origin as the current page.
 * Protocol-relative URLs ("//evil.com") and foreign origins are rejected
 * to prevent open-redirect attacks.
 */
function isSafeRedirect(url: string): boolean {
  if (url.startsWith('/') && !url.startsWith('//')) return true;
  try {
    const parsed = new URL(url);
    return parsed.origin === window.location.origin;
  } catch {
    return false;
  }
}

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

const refreshMiddleware = createRefreshMiddleware(refreshAccessToken);
sdk.use(refreshMiddleware);
authSdk.use(refreshMiddleware);
