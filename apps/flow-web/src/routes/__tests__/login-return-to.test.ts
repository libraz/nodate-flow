/**
 * /login echoes `returnTo` back into the accounts-web redirect after
 * sign-in, so it is an open-redirect surface. The route accepts a
 * returnTo only when it resolves inside this app's own origin.
 *
 * The inputs below are driven through the route's real `validateSearch`
 * rather than matched against the call site, so a rewrite that keeps the
 * shape but drops the check still fails here.
 */

import { describe, expect, it } from 'vitest';

import { type LoginSearch, Route } from '../login';

const validateSearch = Route.options.validateSearch as (
  search: Record<string, unknown>,
) => LoginSearch;

describe('/login returnTo validation', () => {
  it('keeps an in-app path such as the invite landing page', () => {
    expect(validateSearch({ returnTo: '/invite/tok-123' })).toEqual({
      returnTo: '/invite/tok-123',
    });
  });

  it('keeps a path that carries its own query and fragment', () => {
    const raw = '/workspaces/ws-1/projects/p-1?tab=board#task-9';
    expect(validateSearch({ returnTo: raw })).toEqual({ returnTo: raw });
  });

  it.each([
    ['an absolute cross-origin URL', 'https://evil.example/steal'],
    ['a protocol-relative URL', '//evil.example/steal'],
    ['a backslash-escaped path', '/\\evil.example/steal'],
    ['a percent-encoded backslash', '/%5Cevil.example/steal'],
    ['a javascript: URL', 'javascript:alert(1)'],
    ['a data: URL', 'data:text/html,<script>alert(1)</script>'],
  ])('drops %s', (_label, raw) => {
    expect(validateSearch({ returnTo: raw })).toEqual({});
  });

  it('drops a returnTo that is not a string', () => {
    expect(validateSearch({ returnTo: 42 })).toEqual({});
    expect(validateSearch({})).toEqual({});
  });
});
