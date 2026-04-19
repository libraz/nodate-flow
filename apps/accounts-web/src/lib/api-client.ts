/**
 * API client for the auth-api backend (port 8082). Uses plain fetch
 * with automatic token injection and 401 retry via refresh.
 *
 * The auth-api uses the httpOnly `nd_rt` cookie (path `/auth`) for
 * refresh tokens. On 401, we attempt a single refresh and replay.
 */

import { authStore } from '../stores/auth-store';

const baseUrl =
  (import.meta.env.VITE_AUTH_API_BASE_URL as string | undefined) ?? 'http://localhost:8082';

/** Resolved base URL for the auth API. */
export const authApiBaseUrl = baseUrl;

interface ApiRequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
}

interface ApiResponse<T> {
  data: T | null;
  error: ProblemJson | null;
  status: number;
}

/** RFC 7807 problem+json shape. */
export interface ProblemJson {
  type?: string;
  title?: string;
  detail?: string;
  status?: number;
}

/**
 * Low-level fetch wrapper that injects the Authorization header from
 * the auth store and sends credentials (for the nd_rt cookie).
 */
async function rawFetch<T>(path: string, options: ApiRequestOptions = {}): Promise<ApiResponse<T>> {
  const { method = 'GET', body, headers = {} } = options;
  const token = authStore.getState().accessToken;
  const reqHeaders: Record<string, string> = {
    'Content-Type': 'application/json',
    ...headers,
  };
  if (token) {
    reqHeaders.Authorization = `Bearer ${token}`;
  }

  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers: reqHeaders,
    credentials: 'include',
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });

  if (res.status === 204) {
    return { data: null, error: null, status: res.status };
  }

  const contentType = res.headers.get('Content-Type') ?? '';
  if (!contentType.includes('json')) {
    if (!res.ok) {
      return {
        data: null,
        error: { status: res.status, title: res.statusText },
        status: res.status,
      };
    }
    return { data: null, error: null, status: res.status };
  }

  const json = await res.json();
  if (!res.ok) {
    return { data: null, error: json as ProblemJson, status: res.status };
  }
  return { data: json as T, error: null, status: res.status };
}

/**
 * Calls POST /auth/refresh directly. On success, updates the auth store
 * and returns the new access token. On failure, clears the session and
 * returns null.
 *
 * The in-flight promise is memoized so concurrent 401s collapse into a
 * single refresh round-trip. A grace window prevents thundering-herd
 * loops after token rotation.
 */
const REFRESH_GRACE_MS = 1500;
let refreshInFlight: Promise<string | null> | null = null;

export function refreshAccessToken(): Promise<string | null> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      const res = await fetch(`${baseUrl}/auth/refresh`, {
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
  const cleared = refreshInFlight;
  void cleared.finally(() => {
    setTimeout(() => {
      if (refreshInFlight === cleared) refreshInFlight = null;
    }, REFRESH_GRACE_MS);
  });
  return refreshInFlight;
}

/** Paths that should NOT trigger automatic refresh on 401. */
const noRefreshPaths = ['/auth/refresh', '/auth/login', '/auth/register', '/auth/logout'];

/**
 * Public API request function with automatic 401 retry. On a 401
 * response, attempts a single token refresh and replays the request.
 */
export async function apiRequest<T>(
  path: string,
  options: ApiRequestOptions = {},
): Promise<ApiResponse<T>> {
  const result = await rawFetch<T>(path, options);
  if (result.status !== 401) return result;

  if (noRefreshPaths.some((p) => path.startsWith(p))) return result;

  const newToken = await refreshAccessToken();
  if (!newToken) return result;

  return rawFetch<T>(path, options);
}
