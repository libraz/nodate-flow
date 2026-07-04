import { describe, expect, it, vi } from 'vitest';

import { createAuthenticatedFetch, extractRefreshTokenFromSetCookie } from '../src/api.js';

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
