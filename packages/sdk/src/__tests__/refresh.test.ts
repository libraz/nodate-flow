import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createAuthRequestMiddleware,
  createRefreshMiddleware,
  createTokenRefresher,
  decodeTokenExp,
  type RefreshMiddlewareOptions,
} from '../refresh';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeOptions(overrides: Partial<RefreshMiddlewareOptions> = {}): RefreshMiddlewareOptions {
  return {
    authApiBaseUrl: 'https://auth.example.com',
    getAccessToken: vi.fn(() => 'old-token'),
    setAccessToken: vi.fn(),
    clearSession: vi.fn(),
    ...overrides,
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

// ---------------------------------------------------------------------------
// createTokenRefresher
// ---------------------------------------------------------------------------

describe('createTokenRefresher', () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('calls POST /auth/refresh and stores the returned token', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      jsonResponse({ accessToken: 'new-token-abc' }),
    );

    const refresh = createTokenRefresher(opts);
    const result = await refresh();

    expect(result).toBe('new-token-abc');
    expect(globalThis.fetch).toHaveBeenCalledWith('https://auth.example.com/auth/refresh', {
      method: 'POST',
      credentials: 'include',
    });
    expect(opts.setAccessToken).toHaveBeenCalledWith('new-token-abc');
    expect(opts.clearSession).not.toHaveBeenCalled();
  });

  it('calls clearSession and returns null on non-ok response', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(jsonResponse({ error: 'forbidden' }, 403));

    const refresh = createTokenRefresher(opts);
    const result = await refresh();

    expect(result).toBeNull();
    expect(opts.clearSession).toHaveBeenCalledOnce();
    expect(opts.setAccessToken).not.toHaveBeenCalled();
  });

  it('keeps the session when the request never reached the server', async () => {
    const onSessionExpired = vi.fn();
    const opts = makeOptions({ onSessionExpired });
    // What `fetch` throws when the network is down: no response, so
    // nothing was learned about whether the refresh cookie is still good.
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new TypeError('Failed to fetch'));

    const refresh = createTokenRefresher(opts);
    const result = await refresh();

    expect(result).toBeNull();
    expect(opts.clearSession).not.toHaveBeenCalled();
    expect(onSessionExpired).not.toHaveBeenCalled();
    expect(refresh.lastFailure()).toBe('network');
  });

  it('keeps the session when the request was aborted', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(
      new DOMException('The operation was aborted.', 'AbortError'),
    );

    const refresh = createTokenRefresher(opts);

    expect(await refresh()).toBeNull();
    expect(opts.clearSession).not.toHaveBeenCalled();
    expect(refresh.lastFailure()).toBe('network');
  });

  it('ends the session when the server declines, and says so', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(jsonResponse({ error: 'expired' }, 401));

    const refresh = createTokenRefresher(opts);

    expect(await refresh()).toBeNull();
    expect(opts.clearSession).toHaveBeenCalledOnce();
    // The distinction is the whole point: a refused refresh is an answer,
    // a dropped connection is not.
    expect(refresh.lastFailure()).toBe('rejected');
  });

  it('ends the session on a non-transport throw, which is not a connectivity problem', async () => {
    const opts = makeOptions();
    // e.g. the response body failed to parse as JSON.
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new SyntaxError('Unexpected token'));

    const refresh = createTokenRefresher(opts);

    expect(await refresh()).toBeNull();
    expect(opts.clearSession).toHaveBeenCalledOnce();
    expect(refresh.lastFailure()).toBe('rejected');
  });

  it('reports no failure after a successful refresh', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(jsonResponse({ accessToken: 'ok-token' }));

    const refresh = createTokenRefresher(opts);

    expect(await refresh()).toBe('ok-token');
    expect(refresh.lastFailure()).toBe('none');
  });

  it('calls clearSession and returns null when accessToken is missing from body', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(jsonResponse({}));

    const refresh = createTokenRefresher(opts);
    const result = await refresh();

    expect(result).toBeNull();
    expect(opts.clearSession).toHaveBeenCalledOnce();
    expect(opts.setAccessToken).not.toHaveBeenCalled();
  });

  it('invokes onSessionExpired callback after clearSession on failure', async () => {
    const onSessionExpired = vi.fn();
    const opts = makeOptions({ onSessionExpired });
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(jsonResponse({ error: 'expired' }, 401));

    const refresh = createTokenRefresher(opts);
    await refresh();

    expect(opts.clearSession).toHaveBeenCalledOnce();
    expect(onSessionExpired).toHaveBeenCalledOnce();
  });

  it('deduplicates concurrent calls into a single fetch', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      jsonResponse({ accessToken: 'deduped-token' }),
    );

    const refresh = createTokenRefresher(opts);
    const [r1, r2, r3] = await Promise.all([refresh(), refresh(), refresh()]);

    expect(r1).toBe('deduped-token');
    expect(r2).toBe('deduped-token');
    expect(r3).toBe('deduped-token');
    expect(globalThis.fetch).toHaveBeenCalledTimes(1);
  });

  it('reuses the resolved promise within the grace window', async () => {
    vi.useFakeTimers();
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockImplementation(() =>
      Promise.resolve(jsonResponse({ accessToken: 'grace-token' })),
    );

    const refresh = createTokenRefresher(opts);

    // First call resolves
    const first = await refresh();
    expect(first).toBe('grace-token');
    expect(globalThis.fetch).toHaveBeenCalledTimes(1);

    // Advance less than 1500ms -- should reuse the same promise
    vi.advanceTimersByTime(1000);
    const second = await refresh();
    expect(second).toBe('grace-token');
    expect(globalThis.fetch).toHaveBeenCalledTimes(1);

    // Advance past the grace window (total 1600ms)
    vi.advanceTimersByTime(600);

    // Now a new call should trigger a fresh fetch
    const third = await refresh();
    expect(third).toBe('grace-token');
    expect(globalThis.fetch).toHaveBeenCalledTimes(2);

    vi.useRealTimers();
  });
});

// ---------------------------------------------------------------------------
// createRefreshMiddleware
// ---------------------------------------------------------------------------

describe('createRefreshMiddleware', () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('passes through non-401 responses unchanged', async () => {
    const refreshFn = vi.fn();
    const mw = createRefreshMiddleware(refreshFn);

    const request = new Request('https://api.example.com/tasks');
    const response = new Response('ok', { status: 200 });

    const result = await mw.onResponse({ request, response });

    expect(result).toBe(response);
    expect(refreshFn).not.toHaveBeenCalled();
  });

  it('does NOT retry 401 on /auth/refresh path', async () => {
    const refreshFn = vi.fn();
    const mw = createRefreshMiddleware(refreshFn);

    const request = new Request('https://api.example.com/auth/refresh');
    const response = new Response('unauthorized', { status: 401 });

    const result = await mw.onResponse({ request, response });

    expect(result).toBe(response);
    expect(refreshFn).not.toHaveBeenCalled();
  });

  it('does NOT retry 401 on /auth/login path', async () => {
    const refreshFn = vi.fn();
    const mw = createRefreshMiddleware(refreshFn);

    const request = new Request('https://api.example.com/auth/login');
    const response = new Response('unauthorized', { status: 401 });

    const result = await mw.onResponse({ request, response });

    expect(result).toBe(response);
    expect(refreshFn).not.toHaveBeenCalled();
  });

  it('does NOT retry 401 on /auth/register path', async () => {
    const refreshFn = vi.fn();
    const mw = createRefreshMiddleware(refreshFn);

    const request = new Request('https://api.example.com/auth/register');
    const response = new Response('unauthorized', { status: 401 });

    const result = await mw.onResponse({ request, response });

    expect(result).toBe(response);
    expect(refreshFn).not.toHaveBeenCalled();
  });

  it('does NOT retry 401 on /auth/logout path', async () => {
    const refreshFn = vi.fn();
    const mw = createRefreshMiddleware(refreshFn);

    const request = new Request('https://api.example.com/auth/logout');
    const response = new Response('unauthorized', { status: 401 });

    const result = await mw.onResponse({ request, response });

    expect(result).toBe(response);
    expect(refreshFn).not.toHaveBeenCalled();
  });

  it('retries 401 on regular paths with new Bearer token after successful refresh', async () => {
    const refreshFn = vi.fn().mockResolvedValueOnce('refreshed-token');
    const mw = createRefreshMiddleware(refreshFn);

    const replayedResponse = new Response('{"data":"ok"}', { status: 200 });
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(replayedResponse);

    const request = new Request('https://api.example.com/tasks', {
      headers: { authorization: 'Bearer expired-token' },
    });
    const response = new Response('unauthorized', { status: 401 });

    const result = await mw.onResponse({ request, response });

    expect(refreshFn).toHaveBeenCalledOnce();
    expect(result).toBe(replayedResponse);

    // Verify the replayed request used the new token
    const replayedRequest = vi.mocked(globalThis.fetch).mock.calls[0]?.[0] as Request;
    expect(replayedRequest.headers.get('Authorization')).toBe('Bearer refreshed-token');
    expect(replayedRequest.url).toBe('https://api.example.com/tasks');
  });

  it('returns original 401 response when refreshFn returns null', async () => {
    const refreshFn = vi.fn().mockResolvedValueOnce(null);
    const mw = createRefreshMiddleware(refreshFn);

    const request = new Request('https://api.example.com/tasks');
    const response = new Response('unauthorized', { status: 401 });

    const result = await mw.onResponse({ request, response });

    expect(refreshFn).toHaveBeenCalledOnce();
    expect(result).toBe(response);
    expect(globalThis.fetch).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// decodeTokenExp
// ---------------------------------------------------------------------------

/**
 * Encode an ASCII JSON string as base64url (no padding) without relying
 * on `Buffer`, mirroring the runtime-agnostic decoder in the SDK.
 */
function toBase64Url(json: string): string {
  return btoa(json).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** Mint a fake JWT with a given exp (unix seconds). Signature is unused. */
function makeJwt(exp: number): string {
  const header = toBase64Url(JSON.stringify({ alg: 'none', typ: 'JWT' }));
  const payload = toBase64Url(JSON.stringify({ exp }));
  return `${header}.${payload}.sig`;
}

describe('decodeTokenExp', () => {
  it('returns the numeric exp claim from a well-formed JWT', () => {
    const token = makeJwt(1_700_000_000);
    expect(decodeTokenExp(token)).toBe(1_700_000_000);
  });

  it('returns null when the token is not a JWT', () => {
    expect(decodeTokenExp('opaque-token')).toBeNull();
  });

  it('returns null when the payload is not valid base64url JSON', () => {
    expect(decodeTokenExp('aaa.!!!.bbb')).toBeNull();
  });

  it('returns null when exp is missing or non-numeric', () => {
    const noExp = `aaa.${toBase64Url(JSON.stringify({ sub: 'x' }))}.bbb`;
    expect(decodeTokenExp(noExp)).toBeNull();
    const stringExp = `aaa.${toBase64Url(JSON.stringify({ exp: 'soon' }))}.bbb`;
    expect(decodeTokenExp(stringExp)).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// createTokenRefresher — expiry helpers
// ---------------------------------------------------------------------------

describe('createTokenRefresher expiry helpers', () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('isExpiringSoon returns true when no token is present', () => {
    const opts = makeOptions({ getAccessToken: () => undefined });
    const refresh = createTokenRefresher(opts);
    expect(refresh.isExpiringSoon()).toBe(true);
  });

  it('isExpiringSoon returns false for a fresh JWT far from expiry', () => {
    const farFuture = Math.floor(Date.now() / 1000) + 3600; // 1 hour
    const opts = makeOptions({ getAccessToken: () => makeJwt(farFuture) });
    const refresh = createTokenRefresher(opts);
    expect(refresh.isExpiringSoon()).toBe(false);
  });

  it('isExpiringSoon returns true when the JWT is inside the 10s buffer', () => {
    const nearExpiry = Math.floor(Date.now() / 1000) + 5;
    const opts = makeOptions({ getAccessToken: () => makeJwt(nearExpiry) });
    const refresh = createTokenRefresher(opts);
    expect(refresh.isExpiringSoon()).toBe(true);
  });

  it('isExpiringSoon returns false for an opaque (non-JWT) token', () => {
    // Opaque tokens have no decodable exp. The caller opted into opaque
    // bearers; we should not probe refresh on every request for them,
    // and rely on the reactive 401 path instead.
    const opts = makeOptions({ getAccessToken: () => 'opaque' });
    const refresh = createTokenRefresher(opts);
    expect(refresh.isExpiringSoon()).toBe(false);
  });

  it('caches exp for subsequent calls and updates when the token rotates', async () => {
    const nowSec = Math.floor(Date.now() / 1000);
    let current: string | undefined = makeJwt(nowSec + 3600);
    const opts = makeOptions({
      getAccessToken: () => current,
      setAccessToken: (t) => {
        current = t;
      },
    });
    const refresh = createTokenRefresher(opts);

    // First call reads the pre-existing token.
    expect(refresh.currentExp()).toBe(nowSec + 3600);

    // Simulate a refresh returning a new exp.
    const newExp = nowSec + 7200;
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      new Response(JSON.stringify({ accessToken: makeJwt(newExp) }), { status: 200 }),
    );
    await refresh();
    expect(refresh.currentExp()).toBe(newExp);
  });
});

// ---------------------------------------------------------------------------
// createAuthRequestMiddleware
// ---------------------------------------------------------------------------

describe('createAuthRequestMiddleware', () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('attaches the current Authorization header for non-auth paths', async () => {
    const nowSec = Math.floor(Date.now() / 1000);
    const freshToken = makeJwt(nowSec + 3600);
    const opts = makeOptions({ getAccessToken: () => freshToken });
    const refresher = createTokenRefresher(opts);

    const mw = createAuthRequestMiddleware({
      getAccessToken: () => freshToken,
      refresher,
    });

    const request = new Request('https://api.example.com/tasks');
    const result = await mw.onRequest({ request });

    expect(result.headers.get('Authorization')).toBe(`Bearer ${freshToken}`);
    expect(globalThis.fetch).not.toHaveBeenCalled(); // no refresh needed
  });

  it('awaits a refresh before dispatching when the token is near-expiry', async () => {
    const nowSec = Math.floor(Date.now() / 1000);
    const nearExpiryToken = makeJwt(nowSec + 2); // inside 10s buffer
    const rotatedExp = nowSec + 900;
    const rotated = makeJwt(rotatedExp);

    let currentToken: string | undefined = nearExpiryToken;
    const opts = makeOptions({
      getAccessToken: () => currentToken,
      setAccessToken: (t) => {
        currentToken = t;
      },
    });
    const refresher = createTokenRefresher(opts);

    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      new Response(JSON.stringify({ accessToken: rotated }), { status: 200 }),
    );

    const mw = createAuthRequestMiddleware({
      getAccessToken: () => currentToken,
      refresher,
    });

    const request = new Request('https://api.example.com/tasks');
    const result = await mw.onRequest({ request });

    expect(globalThis.fetch).toHaveBeenCalledWith(
      'https://auth.example.com/auth/refresh',
      expect.objectContaining({ method: 'POST' }),
    );
    expect(result.headers.get('Authorization')).toBe(`Bearer ${rotated}`);
  });

  it('skips refresh for auth paths even when the token is near-expiry', async () => {
    const nowSec = Math.floor(Date.now() / 1000);
    const nearExpiry = makeJwt(nowSec + 1);
    const opts = makeOptions({ getAccessToken: () => nearExpiry });
    const refresher = createTokenRefresher(opts);

    const mw = createAuthRequestMiddleware({
      getAccessToken: () => nearExpiry,
      refresher,
    });

    const request = new Request('https://auth.example.com/auth/refresh', { method: 'POST' });
    await mw.onRequest({ request });

    expect(globalThis.fetch).not.toHaveBeenCalled();
  });

  it('does not set Authorization when no token is available after refresh attempt', async () => {
    const opts = makeOptions({ getAccessToken: () => undefined });
    const refresher = createTokenRefresher(opts);

    // Refresh fails (no valid cookie).
    vi.mocked(globalThis.fetch).mockResolvedValueOnce(
      new Response('unauthorized', { status: 401 }),
    );

    const mw = createAuthRequestMiddleware({
      getAccessToken: () => undefined,
      refresher,
    });

    const request = new Request('https://api.example.com/tasks');
    const result = await mw.onRequest({ request });

    expect(result.headers.has('Authorization')).toBe(false);
  });
});
