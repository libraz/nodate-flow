import { describe, expect, it, vi } from 'vitest';

import { createAuthenticatedFetch } from '../src/api.js';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function refreshedResponse(accessToken = 'new-access') {
  return new Response(JSON.stringify({ accessToken }), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
}

describe('createAuthenticatedFetch refresh concurrency', () => {
  it('shares one refresh across simultaneous 401 responses', async () => {
    let accessToken = 'old-access';
    let refreshToken = 'old-refresh';
    let refreshCalls = 0;
    const refreshStarted = deferred<void>();
    const allowRefresh = deferred<void>();
    const setTokens = vi.fn((tokens: { accessToken: string; refreshToken?: string }) => {
      accessToken = tokens.accessToken;
      refreshToken = tokens.refreshToken ?? refreshToken;
    });
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        refreshCalls++;
        refreshStarted.resolve();
        await allowRefresh.promise;
        return new Response(JSON.stringify({ accessToken: 'new-access' }), {
          status: 200,
          headers: { 'set-cookie': 'nd_rt=new-refresh; Path=/auth; HttpOnly' },
        });
      }

      if (request.headers.get('authorization') === 'Bearer old-access') {
        return new Response('expired', { status: 401 });
      }
      return new Response('ok', { status: 200 });
    });
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => refreshToken,
      setTokens,
      fetchImpl,
    });

    const requests = Promise.all([
      authenticatedFetch('http://flow.test/first'),
      authenticatedFetch('http://flow.test/second'),
    ]);
    await refreshStarted.promise;
    allowRefresh.resolve();

    const responses = await requests;

    expect(responses.map((response) => response.status)).toEqual([200, 200]);
    expect(refreshCalls).toBe(1);
    expect(setTokens).toHaveBeenCalledTimes(1);
    expect(setTokens).toHaveBeenCalledWith({
      accessToken: 'new-access',
      refreshToken: 'new-refresh',
    });
  });

  it('retries a delayed old-token 401 with the current token without another refresh', async () => {
    let accessToken = 'old-access';
    let refreshCalls = 0;
    const slowResponse = deferred<Response>();
    const refreshFinished = deferred<void>();
    const setTokens = vi.fn((tokens: { accessToken: string }) => {
      accessToken = tokens.accessToken;
      refreshFinished.resolve();
    });
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        refreshCalls++;
        return refreshedResponse();
      }
      if (
        request.url === 'http://flow.test/slow' &&
        request.headers.get('authorization') === 'Bearer old-access'
      ) {
        return slowResponse.promise;
      }
      if (request.headers.get('authorization') === 'Bearer old-access') {
        return new Response('expired', { status: 401 });
      }
      return new Response('ok', { status: 200 });
    });
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => 'refresh-token',
      setTokens,
      fetchImpl,
    });

    const slowRequest = authenticatedFetch('http://flow.test/slow');
    const fastRequest = authenticatedFetch('http://flow.test/fast');
    await refreshFinished.promise;
    slowResponse.resolve(new Response('expired', { status: 401 }));

    const [slowResponseResult, fastResponseResult] = await Promise.all([slowRequest, fastRequest]);

    expect(slowResponseResult.status).toBe(200);
    expect(fastResponseResult.status).toBe(200);
    expect(refreshCalls).toBe(1);
    const slowRetry = fetchImpl.mock.calls.find(
      ([input]) =>
        input instanceof Request &&
        input.url === 'http://flow.test/slow' &&
        input.headers.get('authorization') === 'Bearer new-access',
    );
    expect(slowRetry).toBeDefined();
  });

  it('clears a failed refresh so a later request can refresh again', async () => {
    let refreshCalls = 0;
    let originalUnauthorized: Response | undefined;
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        refreshCalls++;
        return refreshCalls === 1
          ? new Response('unavailable', { status: 503 })
          : refreshedResponse();
      }
      if (request.headers.get('authorization') === 'Bearer new-access') {
        return new Response('ok', { status: 200 });
      }
      if (refreshCalls === 0) {
        originalUnauthorized = new Response('expired', { status: 401 });
        return originalUnauthorized;
      }
      return new Response('expired', { status: 401 });
    });
    let accessToken = 'old-access';
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => 'refresh-token',
      setTokens: ({ accessToken: updatedToken }) => {
        accessToken = updatedToken;
      },
      fetchImpl,
    });

    const firstResponse = await authenticatedFetch('http://flow.test/first');
    const secondResponse = await authenticatedFetch('http://flow.test/second');

    expect(firstResponse.status).toBe(401);
    expect(firstResponse).toBe(originalUnauthorized);
    expect(secondResponse.status).toBe(200);
    expect(refreshCalls).toBe(2);
  });

  it('propagates refresh errors and clears a rejected refresh before a later request', async () => {
    let refreshCalls = 0;
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        refreshCalls++;
        if (refreshCalls === 1) throw new Error('network unavailable');
        return refreshedResponse();
      }
      if (request.headers.get('authorization') === 'Bearer new-access') {
        return new Response('ok', { status: 200 });
      }
      return new Response('expired', { status: 401 });
    });
    let accessToken = 'old-access';
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => 'refresh-token',
      setTokens: ({ accessToken: updatedToken }) => {
        accessToken = updatedToken;
      },
      fetchImpl,
    });

    await expect(authenticatedFetch('http://flow.test/first')).rejects.toThrow(
      'network unavailable',
    );
    const secondResponse = await authenticatedFetch('http://flow.test/second');

    expect(secondResponse.status).toBe(200);
    expect(refreshCalls).toBe(2);
  });

  it('returns a retried 401 without attempting another refresh', async () => {
    let refreshCalls = 0;
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        refreshCalls++;
        return refreshedResponse();
      }
      return new Response('expired', { status: 401 });
    });
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => 'old-access',
      getRefreshToken: () => 'refresh-token',
      setTokens: vi.fn(),
      fetchImpl,
    });

    const response = await authenticatedFetch('http://flow.test/tasks');

    expect(response.status).toBe(401);
    expect(refreshCalls).toBe(1);
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it('preserves a POST body and headers when retrying after refresh', async () => {
    const bodies: string[] = [];
    let accessToken = 'old-access';
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') return refreshedResponse();
      bodies.push(await request.text());
      if (request.headers.get('authorization') === 'Bearer old-access') {
        return new Response('expired', { status: 401 });
      }
      expect(request.method).toBe('POST');
      expect(request.headers.get('content-type')).toBe('application/json');
      return new Response('created', { status: 201 });
    });
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => 'refresh-token',
      setTokens: ({ accessToken: updatedToken }) => {
        accessToken = updatedToken;
      },
      fetchImpl,
    });

    const response = await authenticatedFetch('http://flow.test/tasks', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-request-id': 'request-1' },
      body: JSON.stringify({ title: 'Create task' }),
    });

    expect(response.status).toBe(201);
    expect(bodies).toEqual(['{"title":"Create task"}', '{"title":"Create task"}']);
    const retry = fetchImpl.mock.calls.find(
      ([input]) =>
        input instanceof Request && input.headers.get('authorization') === 'Bearer new-access',
    );
    const retryRequest = retry?.[0];
    if (!(retryRequest instanceof Request)) {
      throw new Error('Expected the retried input to be a Request');
    }
    expect(retryRequest.headers.get('x-request-id')).toBe('request-1');
  });

  it('adopts tokens another process rotated instead of presenting a stale one', async () => {
    let accessToken = 'old-access';
    let refreshToken = 'old-refresh';
    const presented: string[] = [];
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        presented.push(request.headers.get('cookie') ?? '');
        return refreshedResponse('unexpected');
      }
      return request.headers.get('authorization') === 'Bearer peer-access'
        ? new Response('ok', { status: 200 })
        : new Response('expired', { status: 401 });
    });
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => refreshToken,
      setTokens: vi.fn(),
      // The shared file already holds what a second process rotated to.
      adoptStoredTokens: () => {
        if (refreshToken === 'peer-refresh') return undefined;
        accessToken = 'peer-access';
        refreshToken = 'peer-refresh';
        return accessToken;
      },
      fetchImpl,
    });

    const response = await authenticatedFetch('http://flow.test/tasks');

    expect(response.status).toBe(200);
    expect(presented).toEqual([]);
  });

  it('holds the shared lock across the read, the refresh and the save', async () => {
    const events: string[] = [];
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(input, init);
      if (request.url === 'http://auth.test/auth/refresh') {
        events.push('refresh');
        return new Response(JSON.stringify({ accessToken: 'new-access' }), {
          status: 200,
          headers: { 'set-cookie': 'nd_rt=new-refresh; Path=/auth; HttpOnly' },
        });
      }
      return request.headers.get('authorization') === 'Bearer new-access'
        ? new Response('ok', { status: 200 })
        : new Response('expired', { status: 401 });
    });
    let accessToken = 'old-access';
    const authenticatedFetch = createAuthenticatedFetch({
      authApiBaseUrl: 'http://auth.test',
      getAccessToken: () => accessToken,
      getRefreshToken: () => 'old-refresh',
      setTokens: (tokens) => {
        events.push('save');
        accessToken = tokens.accessToken;
      },
      adoptStoredTokens: () => {
        events.push('read');
        return undefined;
      },
      lock: async (fn) => {
        events.push('lock');
        try {
          return await fn();
        } finally {
          events.push('unlock');
        }
      },
      fetchImpl,
    });

    expect((await authenticatedFetch('http://flow.test/tasks')).status).toBe(200);
    expect(events).toEqual(['lock', 'read', 'refresh', 'save', 'unlock']);
  });
});
