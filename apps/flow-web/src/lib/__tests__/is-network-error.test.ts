/**
 * isNetworkError — distinguish transport-layer failures from server
 * responses we managed to decode. Drives the network-vs-invalid copy
 * split on public pages.
 */

import { describe, expect, it } from 'vitest';

import { ApiError, isNetworkError } from '../api-error';

describe('isNetworkError', () => {
  it('returns true for a native TypeError (fetch transport failure)', () => {
    expect(isNetworkError(new TypeError('Failed to fetch'))).toBe(true);
  });

  it('returns true for a DOMException AbortError (cancelled request)', () => {
    const abort = new DOMException('cancelled', 'AbortError');
    expect(isNetworkError(abort)).toBe(true);
  });

  it('returns true for an ApiError with no code and no httpStatus', () => {
    const transport = new ApiError(undefined, 'Failed to load shared calendar');
    expect(isNetworkError(transport)).toBe(true);
  });

  it('returns false for an ApiError with a server-returned code', () => {
    const expired = new ApiError('SHARE.SHARE.EXPIRED', 'Share expired', 410);
    expect(isNetworkError(expired)).toBe(false);
  });

  it('returns false for an ApiError carrying only an httpStatus', () => {
    const noCode = new ApiError(undefined, 'Internal server error', 500);
    expect(isNetworkError(noCode)).toBe(false);
  });

  it('returns false for unrelated Error subclasses', () => {
    expect(isNetworkError(new Error('boom'))).toBe(false);
    expect(isNetworkError(new RangeError('out of range'))).toBe(false);
  });

  it('returns false for non-Error values', () => {
    expect(isNetworkError(undefined)).toBe(false);
    expect(isNetworkError(null)).toBe(false);
    expect(isNetworkError('string')).toBe(false);
    expect(isNetworkError({ message: 'plain object' })).toBe(false);
  });
});
