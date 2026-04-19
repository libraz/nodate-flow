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

import {
  type NodateFlowClient,
  createClient,
  createRefreshMiddleware,
  createTokenRefresher,
} from '@nodate-flow/sdk';

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
authSdk.use(refreshMiddleware);
