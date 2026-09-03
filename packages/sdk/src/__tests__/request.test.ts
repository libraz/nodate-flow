/**
 * The entry requester is the only place a failed API call is turned
 * into something the UI can act on, so these cases are about what
 * survives that conversion and what counts as a failure in the first
 * place.
 *
 * Two of them are regressions rather than new ground. The hand-written
 * converters this replaced read the envelope for a message and a code
 * and dropped the rest: the HTTP status the terminal-401 handler and
 * the no-retry-on-4xx rule both read, the translation key that decides
 * which sentence the user sees, the RFC 9457 extensions, the recovery
 * hint. And they decided on `error` alone, so a refusal that arrived
 * without a body — a bodyless 403, chi's 405 for an unregistered
 * method, a gateway 502 — reached the success path.
 */

import { describe, expect, it, vi } from 'vitest';

import { ApiError } from '../api-error.js';
import type { NodateFlowClient } from '../client.js';
import { createApiRequester, requestFailed } from '../request.js';

/** The requester never touches the client itself; it only passes it on. */
const client = {} as NodateFlowClient;

function ok<T>(data: T, status = 200) {
  return Promise.resolve({ data, response: new Response(null, { status }) });
}

function refused(status: number, error?: unknown) {
  return Promise.resolve({
    ...(error === undefined ? {} : { error }),
    response: new Response(null, { status }),
  });
}

describe('requestFailed', () => {
  it('counts a non-2xx as a failure even when no error body came with it', () => {
    expect(requestFailed({ response: new Response(null, { status: 403 }) })).toBe(true);
    expect(requestFailed({ response: new Response(null, { status: 405 }) })).toBe(true);
    expect(requestFailed({ response: new Response(null, { status: 502 }) })).toBe(true);
  });

  it('counts a 204 as a success: nothing in the body is the answer', () => {
    expect(requestFailed({ response: new Response(null, { status: 204 }) })).toBe(false);
  });

  it('counts a result with no response at all as a failure', () => {
    expect(requestFailed({})).toBe(true);
    expect(requestFailed({ data: { ok: true } })).toBe(true);
  });
});

describe('apiRequest failure conversion', () => {
  it('keeps the status, code, translation key, extensions and recovery hint', async () => {
    const { request } = createApiRequester(client);
    const promise = request(
      () =>
        refused(409, {
          type: 'WS.TASK.CONFLICT',
          title: 'Conflict',
          detail: 'Someone else changed this task',
          status: 409,
          userAction: 'Reload and try again',
          extensions: { 'x-i18n-key': 'errors:task.conflict', taskId: 't-1' },
        }),
      'Failed to save the task',
    );

    await expect(promise).rejects.toBeInstanceOf(ApiError);
    const err = (await promise.catch((e: unknown) => e)) as ApiError;
    expect(err.httpStatus).toBe(409);
    expect(err.code).toBe('WS.TASK.CONFLICT');
    expect(err.message).toBe('Someone else changed this task');
    expect(err.i18nKey).toBe('errors:task.conflict');
    expect(err.userAction).toBe('Reload and try again');
    expect(err.extensions).toEqual({ 'x-i18n-key': 'errors:task.conflict', taskId: 't-1' });
  });

  it('reads the status off the response line for a 5xx with an envelope', async () => {
    const { request } = createApiRequester(client);
    const err = await request(() => refused(503, { type: 'INTERNAL.UNAVAILABLE' }), 'Failed').catch(
      (e: unknown) => e as ApiError,
    );
    expect((err as ApiError).httpStatus).toBe(503);
  });

  it.each([403, 405, 502])('fails on a %d that carried no error body', async (status) => {
    // These are the refusals that decide on `error` alone missed: a
    // bodyless 403, chi's default 405 for a method the route never
    // registered, a gateway 502. Each arrives with `error` and `data`
    // both undefined.
    const { request } = createApiRequester(client);
    const err = await request(() => refused(status), 'Nothing came back').catch(
      (e: unknown) => e as ApiError,
    );
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).httpStatus).toBe(status);
    expect((err as ApiError).message).toBe('Nothing came back');
  });

  it('treats a 204 as the success it is', async () => {
    const { request } = createApiRequester(client);
    await expect(
      request(
        () => Promise.resolve({ response: new Response(null, { status: 204 }) }),
        'Nothing came back',
      ),
    ).resolves.toBeUndefined();
  });

  it('fails when the result carries no response at all', async () => {
    const { request } = createApiRequester(client);
    const err = await request(
      () => Promise.resolve({ data: { ok: true } }),
      'Unknown outcome',
    ).catch((e: unknown) => e as ApiError);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).message).toBe('Unknown outcome');
  });

  it('rethrows a transport failure unchanged so callers can still recognise it', async () => {
    const { request } = createApiRequester(client);
    const cause = new TypeError('Failed to fetch');
    await expect(request(() => Promise.reject(cause), 'Failed to load')).rejects.toBe(cause);
  });

  it('resolves with the body on success', async () => {
    const { request } = createApiRequester(client);
    await expect(request(() => ok({ tasks: [1, 2] }), 'Failed to load')).resolves.toEqual({
      tasks: [1, 2],
    });
  });
});

describe('the explicit swallow', () => {
  it('stands in for the answer only when the call site asked it to', async () => {
    const { request } = createApiRequester(client);
    await expect(
      request(() => refused(500), 'Failed to load', { onError: 'empty', empty: null }),
    ).resolves.toBeNull();
  });

  it('does not swallow without the option, even for the same failure', async () => {
    const { request } = createApiRequester(client);
    await expect(request(() => refused(500), 'Failed to load')).rejects.toBeInstanceOf(ApiError);
  });

  it('does not stand in for a successful answer', async () => {
    const { request } = createApiRequester(client);
    await expect(
      request(() => ok({ tasks: [] }), 'Failed to load', { onError: 'empty', empty: null }),
    ).resolves.toEqual({ tasks: [] });
  });

  it('covers a transport failure too, which has no response to read', async () => {
    const { request } = createApiRequester(client);
    await expect(
      request(() => Promise.reject(new TypeError('Failed to fetch')), 'Failed to load', {
        onError: 'empty',
        empty: 'gave up',
      }),
    ).resolves.toBe('gave up');
  });
});

describe('probe', () => {
  it('reports the status without treating a non-2xx as a failure', async () => {
    const { probe } = createApiRequester(client);
    await expect(probe(() => refused(404))).resolves.toBe(404);
    await expect(probe(() => ok({}, 200))).resolves.toBe(200);
  });

  it('reports 0 when the request never reached a server', async () => {
    const { probe } = createApiRequester(client);
    await expect(probe(() => Promise.reject(new TypeError('Failed to fetch')))).resolves.toBe(0);
  });
});

describe('settle', () => {
  it('hands back the response of a refusal so headers stay readable', async () => {
    const { settle } = createApiRequester(client);
    const send = () =>
      Promise.resolve({
        error: { type: 'AUTH.LOGIN.RATE_LIMITED' },
        response: new Response(null, { status: 429, headers: { 'Retry-After': '30' } }),
      });
    const outcome = await settle(send, 'Sign-in failed');
    expect(outcome.ok).toBe(false);
    if (outcome.ok) throw new Error('expected a refusal');
    expect(outcome.response?.headers.get('Retry-After')).toBe('30');
    expect(outcome.error).toBeInstanceOf(ApiError);
    expect((outcome.error as ApiError).httpStatus).toBe(429);
  });

  it('still converts the error, so this is a different way of receiving it', async () => {
    const { settle } = createApiRequester(client);
    const outcome = await settle(() => refused(403), 'Refused');
    if (outcome.ok) throw new Error('expected a refusal');
    expect(outcome.error).toBeInstanceOf(ApiError);
    expect((outcome.error as ApiError).httpStatus).toBe(403);
  });
});

describe('the client the requester was built with', () => {
  it('is the one every call receives', async () => {
    const marker = { marker: true } as unknown as NodateFlowClient;
    const send = vi.fn(() => ok({}));
    await createApiRequester(marker).request(send, 'Failed');
    expect(send).toHaveBeenCalledWith(marker);
  });
});
