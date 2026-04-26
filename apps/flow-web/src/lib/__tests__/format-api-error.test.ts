import type { TFunction } from 'i18next';
import { describe, expect, it, vi } from 'vitest';

import { ApiError, formatApiError } from '../api-error';

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

  it('uses ApiError.message when ApiError has no code', () => {
    const t = makeT();
    const err = new ApiError(undefined, 'something exploded');
    expect(formatApiError(err, t, 'fallback.key')).toBe('something exploded');
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
});
