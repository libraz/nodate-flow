// API client factory for the tnk CLI.
// Uses @nodate-flow/sdk with stored credentials.
//
// Runtime values come from the SDK's narrow subpath entry points rather
// than its barrel: the barrel re-exports the React/i18next/zustand
// providers, which are optional peer dependencies the CLI does not
// declare. Type-only imports still use the barrel because they are
// erased before Node ever resolves them.

import { createClient, type NodateFlowClient } from '@nodate-flow/sdk/client';

import {
  type Credentials,
  getAuthApiUrl,
  getFlowApiUrl,
  loadCredentials,
  resolveAuthApiUrl,
  saveCredentials,
} from './config.js';
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
  const needle = `${REFRESH_COOKIE_NAME}=`;
  let searchFrom = 0;
  while (searchFrom < setCookie.length) {
    const start = setCookie.indexOf(needle, searchFrom);
    if (start < 0) return undefined;

    let boundary = start === 0;
    if (!boundary) {
      let prev = start - 1;
      while (prev >= 0 && setCookie[prev] === ' ') prev--;
      boundary = setCookie[prev] === ',';
    }
    if (!boundary) {
      searchFrom = start + needle.length;
      continue;
    }

    const valueStart = start + needle.length;
    let valueEnd = valueStart;
    while (valueEnd < setCookie.length) {
      const ch = setCookie[valueEnd];
      if (ch === ';' || ch === ',') break;
      valueEnd++;
    }
    if (valueEnd === valueStart) return undefined;
    return decodeURIComponent(setCookie.slice(valueStart, valueEnd));
  }
  return undefined;
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
 * Creates a typed SDK client for the service `selectBaseUrl` picks out of
 * the stored credentials, authenticated with the stored access token and
 * able to rotate it on a 401. Throws when no credentials are found.
 */
function createAuthenticatedClient(
  selectBaseUrl: (creds: Credentials) => string,
): NodateFlowClient {
  let creds = loadCredentials();
  if (!creds) {
    throw new AuthRequiredError();
  }
  return createClient({
    baseUrl: selectBaseUrl(creds),
    tokenProvider: () => creds?.accessToken,
    fetchOptions: {
      fetch: createAuthenticatedFetch({
        authApiBaseUrl: resolveAuthApiUrl(creds),
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
 * Creates a typed SDK client for the flow API (tasks, projects,
 * calendar), authenticated with the stored access token.
 */
export function createFlowClient(): NodateFlowClient {
  return createAuthenticatedClient(() => getFlowApiUrl());
}

/**
 * Creates a typed SDK client for the auth API, authenticated with the
 * stored access token. The auth API owns identity and workspace
 * resources (`GET /workspaces` and friends), which the flow API does not
 * serve, so callers of those paths must use this client.
 */
export function createIdentityClient(): NodateFlowClient {
  return createAuthenticatedClient(resolveAuthApiUrl);
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
