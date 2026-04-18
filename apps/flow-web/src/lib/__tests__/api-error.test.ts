import { describe, expect, it } from 'vitest';

import { ApiError, toApiError } from '../api-error';

describe('ApiError', () => {
  it('stores code and message', () => {
    const err = new ApiError('AUTH.EXPIRED', 'Session expired');
    expect(err.code).toBe('AUTH.EXPIRED');
    expect(err.message).toBe('Session expired');
    expect(err.name).toBe('ApiError');
    expect(err).toBeInstanceOf(Error);
  });

  it('handles undefined code', () => {
    const err = new ApiError(undefined, 'Unknown error');
    expect(err.code).toBeUndefined();
    expect(err.message).toBe('Unknown error');
  });
});

describe('toApiError', () => {
  it('extracts detail field from error object', () => {
    const err = toApiError({ detail: 'Not found', type: 'WS.TASK.NOT_FOUND' }, 'fallback');
    expect(err.message).toBe('Not found');
    expect(err.code).toBe('WS.TASK.NOT_FOUND');
  });

  it('falls back to title when detail is missing', () => {
    const err = toApiError({ title: 'Bad Request' }, 'fallback');
    expect(err.message).toBe('Bad Request');
    expect(err.code).toBeUndefined();
  });

  it('uses fallback for non-object errors', () => {
    const err = toApiError('string error', 'fallback message');
    expect(err.message).toBe('fallback message');
    expect(err.code).toBeUndefined();
  });

  it('uses fallback for null', () => {
    const err = toApiError(null, 'fallback');
    expect(err.message).toBe('fallback');
  });

  it('uses fallback when detail and title are non-string', () => {
    const err = toApiError({ detail: 123, title: true }, 'fallback');
    expect(err.message).toBe('fallback');
  });

  it('prefers detail over title', () => {
    const err = toApiError({ detail: 'detail msg', title: 'title msg' }, 'fallback');
    expect(err.message).toBe('detail msg');
  });
});
