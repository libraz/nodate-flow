// API client factory for the tnk CLI.
// Uses @nodate-flow/sdk with stored credentials.

import { createClient, type NodateFlowClient } from '@nodate-flow/sdk';

import { getAuthApiUrl, getFlowApiUrl, loadCredentials, saveCredentials } from './config.js';
import { AuthRequiredError } from './util/exit.js';

const REFRESH_COOKIE_NAME = 'nd_rt';

export interface AuthenticatedFetchOptions {
  authApiBaseUrl: string;
  getAccessToken: () => string;
  getRefreshToken: () => string | undefined;
  setTokens: (tokens: { accessToken: string; refreshToken?: string }) => void;
  fetchImpl?: typeof fetch;
}

/**
 * Extracts the raw nd_rt cookie value from a Set-Cookie header.
 * Node's fetch exposes combined Set-Cookie values through headers.get(),
 * so scan for the cookie name instead of splitting on commas.
 */
export function extractRefreshTokenFromSetCookie(
  setCookie: string | null | undefined,
): string | undefined {
  if (!setCookie) return undefined;
  const match = setCookie.match(/(?:^|,\s*)nd_rt=([^;,]+)/);
  if (!match) return undefined;
  return decodeURIComponent(match[1] as string);
}

function refreshCookieHeader(refreshToken: string): string {
  return `${REFRESH_COOKIE_NAME}=${encodeURIComponent(refreshToken)}`;
}

async function refreshAccessToken(options: AuthenticatedFetchOptions): Promise<string | null> {
  const refreshToken = options.getRefreshToken();
  if (!refreshToken) return null;

  const fetchImpl = options.fetchImpl ?? fetch;
  const response = await fetchImpl(`${options.authApiBaseUrl}/auth/refresh`, {
    method: 'POST',
    headers: {
      accept: 'application/json',
      cookie: refreshCookieHeader(refreshToken),
    },
  });
  if (!response.ok) return null;

  const body = (await response.json()) as { accessToken?: unknown };
  if (typeof body.accessToken !== 'string' || body.accessToken.length === 0) return null;

  const rotatedRefreshToken = extractRefreshTokenFromSetCookie(response.headers.get('set-cookie'));
  options.setTokens(
    rotatedRefreshToken
      ? { accessToken: body.accessToken, refreshToken: rotatedRefreshToken }
      : { accessToken: body.accessToken },
  );
  return body.accessToken;
}

/**
 * Fetch wrapper for the CLI. It retries one 401 response after rotating
 * the refresh cookie value stored in ~/.config/tnk/credentials.json.
 */
export function createAuthenticatedFetch(options: AuthenticatedFetchOptions): typeof fetch {
  const fetchImpl = options.fetchImpl ?? fetch;

  return async (input, init) => {
    const request = new Request(input, init);
    const retryRequest = request.clone();
    request.headers.set('Authorization', `Bearer ${options.getAccessToken()}`);

    const response = await fetchImpl(request);
    if (response.status !== 401) return response;

    const refreshed = await refreshAccessToken(options);
    if (!refreshed) return response;

    retryRequest.headers.set('Authorization', `Bearer ${refreshed}`);
    return fetchImpl(retryRequest);
  };
}

/**
 * Creates a typed SDK client for the flow API, authenticated with
 * the stored access token. Throws when no credentials are found.
 */
export function createFlowClient(): NodateFlowClient {
  let creds = loadCredentials();
  if (!creds) {
    throw new AuthRequiredError();
  }
  return createClient({
    baseUrl: getFlowApiUrl(),
    tokenProvider: () => creds?.accessToken,
    fetchOptions: {
      fetch: createAuthenticatedFetch({
        authApiBaseUrl: getAuthApiUrl(),
        getAccessToken: () => creds?.accessToken ?? '',
        getRefreshToken: () => creds?.refreshToken,
        setTokens: ({ accessToken, refreshToken }) => {
          if (!creds) return;
          creds = {
            ...creds,
            accessToken,
            refreshToken: refreshToken ?? creds.refreshToken,
          };
          saveCredentials(creds);
        },
      }),
    },
  });
}

/**
 * Creates a typed SDK client for the auth API (unauthenticated).
 * Used for login / register flows before credentials exist.
 */
export function createAuthClient(): NodateFlowClient {
  return createClient({
    baseUrl: getAuthApiUrl(),
  });
}
