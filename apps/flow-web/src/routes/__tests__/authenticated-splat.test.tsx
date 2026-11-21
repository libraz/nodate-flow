/**
 * The authenticated splat only catches unmatched paths: concrete public and
 * authenticated routes keep their own match.
 */

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../components/not-found', () => ({ default: () => <p>not found</p> }));

import { createMemoryHistory, createRouter } from '@tanstack/react-router';

import { routeTree } from '../../routeTree.gen';

function matchedRouteIds(path: string): string[] {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
    context: {} as never,
  });
  return router.matchRoutes(path, {}).map((m) => m.routeId);
}

describe('authenticated splat route', () => {
  it.each([
    ['/login', '/login'],
    ['/signup', '/signup'],
    ['/invite/abc', '/invite/$token'],
    ['/share/cal/abc', '/share/cal/$token'],
    ['/today', '/_authenticated/today'],
  ])('%s keeps its own route', (path, routeId) => {
    const ids = matchedRouteIds(path);
    expect(ids).toContain(routeId);
    expect(ids).not.toContain('/_authenticated/$');
  });

  it('matches an unknown URL under the authenticated layout', () => {
    const ids = matchedRouteIds('/no-such-page');
    expect(ids).toContain('/_authenticated/$');
  });

  it('renders the 404 component', async () => {
    const { Route } = await import('../_authenticated.$');
    const Component = Route.options.component as () => React.ReactNode;
    render(<Component />);
    expect(screen.getByText('not found')).toBeTruthy();
  });
});
