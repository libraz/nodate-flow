import { describe, expect, it } from 'vitest';

import { legacyTabTarget, Route } from '../_authenticated.workspaces.$id.projects.$projectId.index';

const params = { id: 'ws-1', projectId: 'p-1' };
const route = Route as unknown as {
  options: {
    validateSearch: (s: Record<string, unknown>) => { tab?: string };
    beforeLoad: (a: { search: { tab?: string }; params: typeof params }) => void;
  };
};

function redirected(tab?: string): { to?: string; replace?: boolean } | null {
  try {
    route.options.beforeLoad({ search: tab === undefined ? {} : { tab }, params });
  } catch (e) {
    return (e as { options: { to?: string; replace?: boolean } }).options;
  }
  return null;
}

describe('legacy ?tab= on the project index', () => {
  it('maps tabs to routes', () => {
    expect(legacyTabTarget('members')).toBe('/workspaces/$id/projects/$projectId/members');
    expect(legacyTabTarget('settings')).toBe('/workspaces/$id/projects/$projectId/settings');
    expect(legacyTabTarget('overview')).toBe('/workspaces/$id/projects/$projectId');
    expect(legacyTabTarget('bogus')).toBe('/workspaces/$id/projects/$projectId');
  });

  it('accepts an optional string tab', () => {
    expect(route.options.validateSearch({ tab: 'members' })).toEqual({ tab: 'members' });
    expect(route.options.validateSearch({ tab: 3 })).toEqual({});
  });

  it('redirects with replace, and not without a tab', () => {
    expect(redirected('members')).toMatchObject({
      to: '/workspaces/$id/projects/$projectId/members',
      replace: true,
    });
    expect(redirected('x')).toMatchObject({ to: '/workspaces/$id/projects/$projectId' });
    expect(redirected()).toBeNull();
  });
});
