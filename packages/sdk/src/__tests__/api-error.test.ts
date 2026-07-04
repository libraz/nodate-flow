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
});
