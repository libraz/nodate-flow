/**
 * Unit tests for the backend-error -> i18n-key mapper and for en/ja/zh
 * locale key parity.
 *
 * What we pin here:
 *   1. The OIDC `PROVIDER_REJECTED` / `EMAIL_NOT_VERIFIED` codes map to
 *      their dedicated keys (regression for the previously-unmapped pair),
 *      and a representative validation / session code keeps mapping to a
 *      specific key rather than the generic fallback.
 *   2. Every i18n key the mapper can emit actually exists in all three
 *      locale bundles (so profile/login errors never render a raw key).
 *   3. The three `auth.json` bundles share an identical key set, which is
 *      the parity guarantee (catches a future
 *      drop-one-locale regression like the zh gap that motivated this).
 */

import { AuthErrors, createApiRequester, type NodateFlowClient } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

import en from '../../../locales/en/auth.json';
import ja from '../../../locales/ja/auth.json';
import zh from '../../../locales/zh/auth.json';
import { mapAuthError, mapAuthThrown } from '../auth-errors';

/** Flattens a nested locale object into dotted leaf-key paths. */
function flattenKeys(obj: Record<string, unknown>, prefix = ''): string[] {
  const out: string[] = [];
  for (const [key, value] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      out.push(...flattenKeys(value as Record<string, unknown>, path));
    } else {
      out.push(path);
    }
  }
  return out;
}

/** Resolves a dotted path against a locale object, or undefined. */
function resolvePath(obj: Record<string, unknown>, path: string): unknown {
  return path.split('.').reduce<unknown>((acc, segment) => {
    if (acc !== null && typeof acc === 'object' && segment in (acc as object)) {
      return (acc as Record<string, unknown>)[segment];
    }
    return undefined;
  }, obj);
}

const enKeys = flattenKeys(en as Record<string, unknown>);
const jaKeys = flattenKeys(ja as Record<string, unknown>);
const zhKeys = flattenKeys(zh as Record<string, unknown>);

describe('mapAuthError', () => {
  it('uses a curated i18n key when the catalog carries one', () => {
    expect(mapAuthError({ type: AuthErrors.AUTH_OIDC_PROVIDER_REJECTED.code })).toBe(
      'auth:errors.oidc_provider_rejected',
    );
  });

  /**
   * A curated key is an override, and an override that resolves to
   * nothing is worse than none: the reader gets the key string itself in
   * a toast while a translated message for the same code sits in the
   * generated bundle. Six codes were in that state, including this one —
   * `auth.errors.session_unauthorized` exists in no locale of either app.
   *
   * This case used to assert the curated key, which is to say it pinned
   * the broken behaviour: the assertion passed precisely because the
   * lookup short-circuited before reaching the message that works.
   */
  it('falls through to the generated code entry when no curated key is set', () => {
    expect(mapAuthError({ type: AuthErrors.AUTH_SESSION_UNAUTHORIZED.code })).toBe(
      'errors:AUTH.SESSION.UNAUTHORIZED',
    );
  });

  it('uses extension i18n keys ahead of catalog and generated error-code fallbacks', () => {
    expect(mapAuthError({ type: AuthErrors.AUTH_OIDC_EMAIL_NOT_VERIFIED.code })).toBe(
      'errors:AUTH.OIDC.EMAIL_NOT_VERIFIED',
    );
    expect(
      mapAuthError({
        type: AuthErrors.AUTH_OIDC_EMAIL_NOT_VERIFIED.code,
        extensions: { 'x-i18n-key': 'auth.errors.oidc_email_not_verified' },
      }),
    ).toBe('auth:errors.oidc_email_not_verified');
  });

  it('maps generated auth catalog codes to the errors namespace instead of unknown', () => {
    const key = mapAuthError({ type: AuthErrors.AUTH_SESSION_REVOKED.code });
    expect(key).toBe('errors:AUTH.SESSION.REVOKED');
    expect(key).not.toBe('auth:errors.generic');
    expect(key).not.toBe('auth:errors.unknown');
  });

  it('keeps every generated auth error code out of the unknown fallback', () => {
    for (const def of Object.values(AuthErrors)) {
      expect(mapAuthError({ type: def.code }), def.code).not.toBe('auth:errors.unknown');
    }
  });

  it('falls back to unknown only for unrecognized codes', () => {
    expect(mapAuthError({ type: 'AUTH.SOMETHING.NOT.A.REAL.CODE' })).toBe('auth:errors.unknown');
    expect(mapAuthError(null)).toBe('auth:errors.unknown');
  });

  it('no longer references the dead totp_challenge_expired key', () => {
    expect(enKeys).not.toContain('errors.totp_challenge_expired');
    expect(jaKeys).not.toContain('errors.totp_challenge_expired');
    expect(zhKeys).not.toContain('errors.totp_challenge_expired');
  });
});

/**
 * The thrown-value mapper, against the three ways a call actually fails.
 *
 * The transport row is the one that moved: the requester now converts a
 * dropped connection into a `NetworkError` before it reaches here, so a
 * mapper that only recognised a bare `TypeError` would have started
 * answering "unknown" for the one failure a reader can do something
 * about — and the screens this app owns are sign-in and two-factor,
 * where "an unexpected error occurred" over a flat network is the
 * difference between waiting and calling for help.
 */
describe('mapAuthThrown', () => {
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

  it('names the network failure when the connection drops', async () => {
    const err = await caught(
      () => Promise.reject(new TypeError('Failed to fetch')),
      'Sign-in failed',
    );
    expect(mapAuthThrown(err)).toBe('auth:errors.network');
  });

  it('still names it for a bare TypeError from a path holding its own fetch', () => {
    expect(mapAuthThrown(new TypeError('Load failed'))).toBe('auth:errors.network');
  });

  it('keeps the catalogue code for a refusal that carries one', async () => {
    const err = await caught(
      () =>
        Promise.resolve({
          error: { type: AuthErrors.AUTH_SESSION_REVOKED.code },
          response: new Response(null, { status: 401 }),
        }),
      'Sign-in failed',
    );
    expect(mapAuthThrown(err)).toBe('errors:AUTH.SESSION.REVOKED');
  });

  it('falls back to a translated sentence when the answer carried no body', async () => {
    const err = await caught(
      () => Promise.resolve({ response: new Response(null, { status: 502 }) }),
      'Sign-in failed',
    );
    const key = mapAuthThrown(err);
    expect(key).toBe('auth:errors.unknown');
    // Whatever it resolves to, it is not the English literal the call
    // site handed the requester.
    expect(key).not.toContain('Sign-in failed');
  });

  it('emits keys that exist in every locale', async () => {
    const network = await caught(
      () => Promise.reject(new TypeError('Failed to fetch')),
      'Sign-in failed',
    );
    for (const key of [mapAuthThrown(network), mapAuthThrown({})]) {
      const path = key.replace('auth:', '');
      for (const bundle of [en, ja, zh]) {
        expect(resolvePath(bundle as Record<string, unknown>, path), key).toBeTypeOf('string');
      }
    }
  });
});

describe('auth locale parity', () => {
  it('ja and zh share the exact key set with en', () => {
    const enSet = new Set(enKeys);
    const jaSet = new Set(jaKeys);
    const zhSet = new Set(zhKeys);

    expect([...jaSet].filter((k) => !enSet.has(k))).toEqual([]);
    expect([...enSet].filter((k) => !jaSet.has(k))).toEqual([]);
    expect([...zhSet].filter((k) => !enSet.has(k))).toEqual([]);
    expect([...enSet].filter((k) => !zhSet.has(k))).toEqual([]);
  });

  it('exposes the keys that regressed in zh', () => {
    const regressed = [
      'rate_limit.banner',
      'signup.password_confirm',
      'signup.success',
      'errors.passwords_do_not_match',
      'security.qr_fallback_title',
      'security.session_revoke_failed',
      'security.session.browser_unknown',
      'security.session.os_unknown',
      'security.totp.recovery.copy_all',
      'security.totp.recovery.download',
      'security.totp.recovery.print',
      'security.totp.recovery.copied',
      'security.totp.recovery.copy_failed',
      'security.totp.recovery.file_header',
    ];
    for (const key of regressed) {
      expect(resolvePath(zh as Record<string, unknown>, key), key).toBeTypeOf('string');
    }
  });
});
