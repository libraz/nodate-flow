/**
 * What a failed request says to the person who caused it.
 *
 * The three cases below are the three ways a call can fail, and each
 * used to end somewhere different. A dropped connection reached the
 * toast as the browser's own sentence — "Failed to fetch" in Chrome,
 * "Load failed" in Safari — printed unchanged in the Japanese and
 * Chinese UIs. A refusal that arrived without a readable body reached
 * it as the English literal the call site handed the requester as its
 * log message. Only the coded refusal was ever translated.
 *
 * So these assert on the absence of those strings as much as on the
 * presence of the new ones: a helper that appended a translation while
 * still leaking the raw text would pass a presence-only check.
 *
 * They run against the real requester rather than hand-built errors,
 * because the conversion under test happens inside it. Building an
 * `ApiError` here by hand would keep passing on the day the requester
 * stops making one.
 */

import { createApiRequester, type NodateFlowClient } from '@nodate-flow/sdk';
import type { TFunction } from 'i18next';
import { describe, expect, it, vi } from 'vitest';

import { ApiError, formatApiError, NetworkError } from '../api-error';

/**
 * Build a stub i18next TFunction that returns a recorded key, optionally
 * honoring `defaultValue` for the unknown-code case. Cast at the boundary
 * so call sites stay strictly typed.
 */
function makeT(translations: Record<string, string> = {}): TFunction {
  const fn = vi.fn((key: string, options?: { defaultValue?: string; ns?: string }): string => {
    const hit = translations[key];
    if (hit !== undefined) return hit;
    if (options?.defaultValue !== undefined) return options.defaultValue;
    return key;
  });
  return fn as unknown as TFunction;
}

/** The requester never touches the client itself; it only passes it on. */
const client = {} as NodateFlowClient;

/** Runs a call through the real requester and hands back what it threw. */
async function caught(send: () => Promise<unknown>, fallback: string): Promise<unknown> {
  const { request } = createApiRequester(client);
  return request(send as never, fallback).then(
    () => {
      throw new Error('expected the call to fail');
    },
    (err: unknown) => err,
  );
}

describe('formatApiError', () => {
  it('translates ApiError with a known code via the errors namespace', () => {
    const t = makeT({ 'WS.TASK.NOT_FOUND': 'Task not found' });
    const err = new ApiError('WS.TASK.NOT_FOUND', 'raw upstream message', 404);
    expect(formatApiError(err, t, 'fallback.key')).toBe('Task not found');
  });

  it('falls back to ApiError.message when the code has no translation', () => {
    const t = makeT();
    const err = new ApiError('WS.UNKNOWN.CODE', 'raw upstream message');
    // makeT returns defaultValue when no translation entry exists.
    expect(formatApiError(err, t, 'fallback.key')).toBe('raw upstream message');
  });

  it('returns Error.message for plain Error instances', () => {
    const t = makeT();
    expect(formatApiError(new Error('boom'), t, 'fallback.key')).toBe('boom');
  });

  it('translates the fallback key for unknown error shapes', () => {
    const t = makeT({ 'fallback.key': 'Something went wrong' });
    expect(formatApiError('string error', t, 'fallback.key')).toBe('Something went wrong');
    expect(formatApiError(null, t, 'fallback.key')).toBe('Something went wrong');
    expect(formatApiError(undefined, t, 'fallback.key')).toBe('Something went wrong');
    expect(formatApiError({ random: true }, t, 'fallback.key')).toBe('Something went wrong');
  });

  describe('the three ways a call fails', () => {
    it('says the server could not be reached when the connection drops', async () => {
      const t = makeT({ 'common.network_error': 'ネットワークエラーです。' });
      const err = await caught(
        () => Promise.reject(new TypeError('Failed to fetch')),
        'Failed to load tasks',
      );
      const message = formatApiError(err, t, 'tasks.errors.load_failed');
      expect(message).toBe('ネットワークエラーです。');
      expect(message).not.toContain('Failed to fetch');
      expect(message).not.toContain('Failed to load tasks');
    });

    it('still renders the catalogue translation for a refusal that carries a code', async () => {
      const t = makeT({ 'WS.TASK.CONFLICT': 'このタスクは他の誰かが更新しました。' });
      const err = await caught(
        () =>
          Promise.resolve({
            error: { type: 'WS.TASK.CONFLICT', detail: 'task was modified' },
            response: new Response(null, { status: 409 }),
          }),
        'Failed to update task',
      );
      const message = formatApiError(err, t, 'tasks.errors.update_failed');
      expect(message).toBe('このタスクは他の誰かが更新しました。');
      expect(message).not.toContain('task was modified');
      expect(message).not.toContain('Failed to update task');
    });

    it('translates the call-site key when the answer carried no readable body', async () => {
      const t = makeT({ 'tasks.errors.update_failed': 'タスクを更新できませんでした。' });
      const err = await caught(
        () => Promise.resolve({ response: new Response(null, { status: 502 }) }),
        'Failed to update task',
      );
      expect(err).toBeInstanceOf(ApiError);
      expect((err as ApiError).httpStatus).toBe(502);
      const message = formatApiError(err, t, 'tasks.errors.update_failed');
      expect(message).toBe('タスクを更新できませんでした。');
      expect(message).not.toContain('Failed to update task');
    });
  });

  it('reads the network sentence from the common namespace, whatever namespace the caller uses', () => {
    const t = makeT({ 'common.network_error': 'ネットワークエラーです。' });
    formatApiError(new NetworkError('Failed to load tasks'), t, 'tasks.errors.load_failed');
    expect(t).toHaveBeenCalledWith('common.network_error', { ns: 'common' });
  });

  it('treats an answerless result as a network failure, not as a refusal', () => {
    const t = makeT({ 'common.network_error': 'ネットワークエラーです。' });
    const noAnswer = new ApiError(undefined, 'Unknown outcome');
    expect(formatApiError(noAnswer, t, 'fallback.key')).toBe('ネットワークエラーです。');
  });
});
