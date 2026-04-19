/**
 * SDK client for the accounts-web app (auth-api backend).
 *
 * Wires the auth store into the openapi-fetch client:
 *
 * 1. tokenProvider injects `Authorization: Bearer <token>` from the auth
 *    store on every request.
 * 2. A response middleware catches 401 responses, attempts a single
 *    refresh against `POST /auth/refresh` (which uses the httpOnly nd_rt
 *    cookie), and replays the original request with the new token.
 *    On refresh failure the auth store is cleared and the original 401
 *    is returned to the caller.
 *
 * The client uses `credentials: 'include'` so the refresh cookie is sent
 * automatically (configured inside `@nodate-flow/sdk`'s createClient).
 */

import { createClient, createRefreshMiddleware, createTokenRefresher } from '@nodate-flow/sdk';

import { authStore } from '../features/auth/auth-store';

/** Base URL of the auth-api service (port 8082). */
export const authApiBaseUrl =
  (import.meta.env.VITE_AUTH_API_BASE_URL as string | undefined) ?? 'http://localhost:8082';

/**
 * Untyped SDK helper. The auth-api endpoints are not in the shared
 * OpenAPI spec, so we expose GET / POST / PATCH / DELETE as loose
 * functions that accept arbitrary paths. Callers cast responses to
 * the expected shape locally.
 */
interface UntypedResponse {
  data?: unknown;
  error?: unknown;
  response: Response;
}

type AnySdk = {
  // biome-ignore lint/suspicious/noExplicitAny: auth-api paths not in OpenAPI spec
  [K in 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE']: (...args: any[]) => Promise<UntypedResponse>;
} & { use: (middleware: unknown) => void };

const typedSdk = createClient({
  baseUrl: authApiBaseUrl,
  tokenProvider: () => authStore.getState().accessToken ?? undefined,
});

export const sdk = typedSdk as unknown as AnySdk;

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

const refreshMiddleware = createRefreshMiddleware(refreshAccessToken);
sdk.use(refreshMiddleware);
