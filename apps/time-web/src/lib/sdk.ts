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

import { createClient } from '@nodate-flow/sdk';

import { authStore } from '../stores/auth-store';

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
 * Attempts a token refresh via auth-api. On success updates the auth
 * store and returns true; on failure clears auth and returns false.
 */
let isRefreshing = false;
let refreshPromise: Promise<boolean> | null = null;

export function tryRefreshToken(): Promise<boolean> {
  if (isRefreshing && refreshPromise) return refreshPromise;
  isRefreshing = true;
  refreshPromise = (async () => {
    try {
      const res = await fetch(`${authApiBaseUrl}/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
      });
      if (!res.ok) return false;
      const data = (await res.json()) as { accessToken: string };
      const { user } = authStore.getState();
      if (user) {
        authStore.getState().setSession(data.accessToken, user);
      } else {
        authStore.setState({ accessToken: data.accessToken });
      }
      return true;
    } catch {
      return false;
    } finally {
      isRefreshing = false;
      refreshPromise = null;
    }
  })();
  return refreshPromise;
}

// Response middleware: on 401, try a single refresh + replay.
const refreshMiddleware = {
  async onResponse({ request, response }: { request: Request; response: Response }) {
    if (response.status !== 401) return response;
    const url = new URL(request.url);
    const path = url.pathname;
    if (path.startsWith('/auth/')) return response;

    const refreshed = await tryRefreshToken();
    if (!refreshed) {
      authStore.getState().clearSession();
      window.location.href = `${accountsWebUrl}/login?redirect=${encodeURIComponent(window.location.href)}`;
      return response;
    }

    const newToken = authStore.getState().accessToken;
    const replay = new Request(request, {
      headers: new Headers(request.headers),
    });
    if (newToken) {
      replay.headers.set('Authorization', `Bearer ${newToken}`);
    }
    return fetch(replay);
  },
};

sdk.use(refreshMiddleware);
authSdk.use(refreshMiddleware);
