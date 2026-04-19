import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  type RefreshMiddlewareOptions,
  createRefreshMiddleware,
  createTokenRefresher,
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

  it('calls clearSession and returns null on network error', async () => {
    const opts = makeOptions();
    vi.mocked(globalThis.fetch).mockRejectedValueOnce(new TypeError('Failed to fetch'));

    const refresh = createTokenRefresher(opts);
    const result = await refresh();

    expect(result).toBeNull();
    expect(opts.clearSession).toHaveBeenCalledOnce();
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
