import { describe, expect, it } from 'vitest';

import { ApiError, toApiError } from '../api-error';

describe('toApiError', () => {
  it('reads x-i18n-key from problem extensions', () => {
    const err = toApiError(
      {
        type: 'AUTH.SESSION.UNAUTHORIZED',
        detail: 'Sign in required',
        status: 401,
        extensions: { 'x-i18n-key': 'auth.errors.session_unauthorized' },
      },
      'fallback',
    );

    expect(err).toBeInstanceOf(ApiError);
    expect(err.i18nKey).toBe('auth.errors.session_unauthorized');
    expect(err.extensions?.['x-i18n-key']).toBe('auth.errors.session_unauthorized');
  });

  it('falls back to the generated catalog i18nKey for known codes', () => {
    const err = toApiError(
      { type: 'AUTH.OIDC.PROVIDER_REJECTED', detail: 'Provider rejected sign-in' },
      'fallback',
    );

    expect(err.i18nKey).toBe('auth.errors.oidc_provider_rejected');
  });

  it('carries the status from a problem+json body when none is passed in', () => {
    // Callers that only hold the parsed body — not the Response — must
    // still get a status, because the terminal-401 handling and the
    // "never retry a 4xx" rule branch on it.
    const err = toApiError(
      { type: 'AUTH.TOKEN.EXPIRED', detail: 'Token expired', status: 401 },
      'fallback',
    );

    expect(err.httpStatus).toBe(401);
    expect(err.code).toBe('AUTH.TOKEN.EXPIRED');
  });

  it('prefers an explicitly passed status over the one in the body', () => {
    const err = toApiError(
      { type: 'WS.TASK.NOT_FOUND', detail: 'Gone', status: 404 },
      'fallback',
      410,
    );

    expect(err.httpStatus).toBe(410);
  });

  it('still reads a legacy {status, code, message} body', () => {
    // Insurance for a body that did not come from this API — a proxy or
    // a gateway in front of it. The server's own emitters are held to
    // problem+json by a guard on that side.
    const err = toApiError(
      { status: 429, code: 'RATE.LIMIT.EXCEEDED', message: 'Slow down' },
      'fallback',
    );

    expect(err.httpStatus).toBe(429);
    expect(err.code).toBe('RATE.LIMIT.EXCEEDED');
    expect(err.message).toBe('Slow down');
  });

  it('uses the fallback message when the body says nothing useful', () => {
    const err = toApiError({}, 'Failed to list tasks', 500);

    expect(err.message).toBe('Failed to list tasks');
    expect(err.code).toBeUndefined();
    expect(err.httpStatus).toBe(500);
  });
});
