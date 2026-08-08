import { createRouter } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import type { RouterContext } from '../../../router/router';
import { routeTree } from '../../../routeTree.gen';
import { WORKSPACE_INVITE_ROUTE, workspaceInvitePath, workspaceInviteUrl } from '../invite-link';

/** Every path pattern the router serves, e.g. `/invite/$token`. */
function declaredRoutePatterns(): string[] {
  // The context is never read here — only the path table is — so an
  // empty one keeps this off the app's i18n and query-client singletons.
  const router = createRouter({ routeTree, context: {} as RouterContext });
  return Object.keys(router.routesByPath);
}

/** Turns a route pattern into a matcher for concrete pathnames. */
function patternToRegExp(pattern: string): RegExp {
  const source = pattern
    .split('/')
    .map((seg) => (seg.startsWith('$') ? '[^/]+' : seg.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    .join('/');
  return new RegExp(`^${source}/?$`);
}

describe('workspace invite link', () => {
  it('builds a URL that resolves to a route the app serves', () => {
    // Matched against the router's own path table rather than a second
    // hand-written copy of the string: two hand-written copies is how
    // the invite email came to point at a route that does not exist.
    const patterns = declaredRoutePatterns();
    expect(patterns).toContain(WORKSPACE_INVITE_ROUTE);

    const built = workspaceInvitePath('inv_abc123');
    const matched = patterns.filter((p) => patternToRegExp(p).test(built));
    expect(matched).toEqual([WORKSPACE_INVITE_ROUTE]);
  });

  it('escapes the token and does not double the separator', () => {
    expect(workspaceInviteUrl('https://flow.example.test', 'inv_abc')).toBe(
      'https://flow.example.test/invite/inv_abc',
    );
    expect(workspaceInviteUrl('https://flow.example.test/', 'inv_abc')).toBe(
      'https://flow.example.test/invite/inv_abc',
    );
    expect(workspaceInvitePath('a/b')).toBe('/invite/a%2Fb');
  });
});
