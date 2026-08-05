import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  createAuthenticatedFetch,
  createFlowClient,
  createIdentityClient,
  extractRefreshTokenFromSetCookie,
} from '../src/api.js';
import { loadCredentials } from '../src/config.js';

// Only the credential file access is faked; the URL resolution helpers
// stay real so the tests exercise the actual env/credential precedence.
vi.mock('../src/config.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../src/config.js')>();
  return {
    ...actual,
    loadCredentials: vi.fn(),
    saveCredentials: vi.fn(),
  };
});

describe('extractRefreshTokenFromSetCookie', () => {
  it('extracts nd_rt from a Set-Cookie header', () => {
    expect(extractRefreshTokenFromSetCookie('nd_rt=refresh-123; Path=/auth; HttpOnly')).toBe(
      'refresh-123',
    );
  });

  it('finds nd_rt in a combined Set-Cookie header', () => {
    expect(
      extractRefreshTokenFromSetCookie(
        'other=value; Path=/, nd_rt=refresh%20value; Path=/auth; HttpOnly',
      ),
    ).toBe('refresh value');
  });

  it('returns undefined when nd_rt is absent', () => {
    expect(extractRefreshTokenFromSetCookie('other=value; Path=/')).toBeUndefined();
    expect(extractRefreshTokenFromSetCookie(null)).toBeUndefined();
  });

  it('does not match cookie names that merely end with nd_rt', () => {
    expect(extractRefreshTokenFromSetCookie('xnd_rt=wrong; Path=/')).toBeUndefined();
    expect(extractRefreshTokenFromSetCookie('xnd_rt=wrong; Path=/, nd_rt=right; Path=/auth')).toBe(
      'right',
    );
  });
});

describe('createAuthenticatedFetch', () => {
  it('refreshes on 401, persists rotated tokens, and retries once', async () => {
    let accessToken = 'old-access';
    let refreshToken = 'old-refresh';
    const setTokens = vi.fn((tokens: { accessToken: string; refreshToken?: string }) => {
      accessToken = tokens.accessToken;
      refreshToken = tokens.refreshToken ?? refreshToken;
    });

    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : String(input);
      if (url === 'http://flow.test/tasks') {
        const request = input as Request;
        if (fetchImpl.mock.calls.length === 1) {
          expect(request.headers.get('authorization')).toBe('Bearer old-access');
          return new Response(JSON.stringify({ detail: 'expired' }), { status: 401 });
        }
        expect(request.headers.get('authorization')).toBe('Bearer new-access');
        return new Response(JSON.stringify({ tasks: [], total: 0 }), { status: 200 });
      }

      expect(url).toBe('http://auth.test/auth/refresh');
      expect(init?.method).toBe('POST');
      expect(new Headers(init?.headers).get('cookie')).toBe('nd_rt=old-refresh');
      return new Response(JSON.stringify({ accessToken: 'new-access' }), {
        status: 200,
        headers: { 'set-cookie': 'nd_rt=new-refresh; Path=/auth; HttpOnly' },
      });
    });

    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => refreshToken,
      setTokens,
      fetchImpl,
    });

    const response = await authenticatedFetch('http://flow.test/tasks');

    expect(response.status).toBe(200);
    expect(fetchImpl).toHaveBeenCalledTimes(3);
    expect(setTokens).toHaveBeenCalledWith({
      accessToken: 'new-access',
      refreshToken: 'new-refresh',
    });
    expect(accessToken).toBe('new-access');
    expect(refreshToken).toBe('new-refresh');
  });

  it('returns the original 401 when no refresh token is stored', async () => {
    const setTokens = vi.fn();
    const fetchImpl = vi.fn(async () => new Response('expired', { status: 401 }));
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => 'old-access',
      getRefreshToken: () => undefined,
      setTokens,
      fetchImpl,
    });

    const response = await authenticatedFetch('http://flow.test/tasks');

    expect(response.status).toBe(401);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(setTokens).not.toHaveBeenCalled();
  });
});

describe('client factories', () => {
  let requestedUrls: string[];

  beforeEach(() => {
    requestedUrls = [];
    vi.mocked(loadCredentials).mockReturnValue({
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
      apiBaseUrl: 'http://stored-flow.test',
      authApiBaseUrl: 'http://stored-auth.test',
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        requestedUrls.push(input instanceof Request ? input.url : String(input));
        return new Response(JSON.stringify({ workspaces: [], total: 0 }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    vi.mocked(loadCredentials).mockReset();
  });

  it('sends identity-client requests to the auth API', async () => {
    vi.stubEnv('NF_AUTH_API_URL', 'http://auth.test');
    vi.stubEnv('NF_FLOW_API_URL', 'http://flow.test');

    await createIdentityClient().GET('/workspaces', { params: { query: { limit: 100 } } });

    expect(requestedUrls).toEqual(['http://auth.test/workspaces?limit=100']);
  });

  it('sends flow-client requests to the flow API', async () => {
    vi.stubEnv('NF_AUTH_API_URL', 'http://auth.test');
    vi.stubEnv('NF_FLOW_API_URL', 'http://flow.test');

    await createFlowClient().GET('/tasks', { params: { query: { limit: 25 } } });

    expect(requestedUrls).toEqual(['http://flow.test/tasks?limit=25']);
  });

  it('falls back to the auth API URL stored at login', async () => {
    vi.stubEnv('NF_AUTH_API_URL', undefined);
    vi.stubEnv('NF_FLOW_API_URL', undefined);

    await createIdentityClient().GET('/workspaces', { params: { query: { limit: 100 } } });

    expect(requestedUrls).toEqual(['http://stored-auth.test/workspaces?limit=100']);
  });

  it('rejects both factories when no credentials are stored', () => {
    vi.mocked(loadCredentials).mockReturnValue(undefined);

    expect(() => createIdentityClient()).toThrow();
    expect(() => createFlowClient()).toThrow();
  });
});
