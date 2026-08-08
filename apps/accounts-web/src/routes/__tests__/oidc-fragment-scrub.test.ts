/**
 * The OIDC hand-off delivers the TOTP step-up challenge in a URL
 * fragment. A fragment is not sent to the server but it does persist:
 * in the history entry, in a restored session, and in whatever the user
 * copies out of the address bar. The challenge is a bearer credential
 * for the first factor, so it must not still be sitting there after the
 * page has read it.
 */

import { afterEach, describe, expect, it } from 'vitest';

import { scrubOIDCFragment } from '../oidc.complete';

afterEach(() => {
  window.history.replaceState(null, '', '/');
});

describe('scrubOIDCFragment', () => {
  it('removes the challenge from the address bar', () => {
    window.history.replaceState(
      null,
      '',
      '/oidc/complete#step=totp_required&challengeToken=secret-challenge',
    );
    expect(window.location.hash).toContain('secret-challenge');

    scrubOIDCFragment();

    expect(window.location.hash).toBe('');
    expect(window.location.href).not.toContain('secret-challenge');
  });

  it('keeps the path and query so the page still knows where it is', () => {
    window.history.replaceState(null, '', '/oidc/complete?foo=bar#challengeToken=x');

    scrubOIDCFragment();

    expect(window.location.pathname).toBe('/oidc/complete');
    expect(window.location.search).toBe('?foo=bar');
  });

  it('leaves an entry with no fragment untouched', () => {
    window.history.replaceState(null, '', '/oidc/complete');
    const before = window.location.href;

    scrubOIDCFragment();

    expect(window.location.href).toBe(before);
  });
});
