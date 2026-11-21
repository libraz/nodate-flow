import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ matches: [] as unknown[] }));

vi.mock('@tanstack/react-router', () => ({ useMatches: () => mocks.matches }));
vi.mock('../../../lib/use-current-workspace', () => ({ useCurrentWorkspaceId: () => null }));

import { useTopBarBreadcrumb } from '../use-top-bar-breadcrumb';

const BASE = '/_authenticated/workspaces/$id/projects/$projectId';

function at(suffix: string): void {
  mocks.matches = [
    {
      routeId: BASE,
      params: { id: 'ws-1', projectId: 'p-1' },
      search: {},
    },
    { routeId: `${BASE}${suffix}`, params: { id: 'ws-1', projectId: 'p-1' }, search: {} },
  ];
}

beforeEach(() => {
  mocks.matches = [];
});

describe('useTopBarBreadcrumb project views', () => {
  it.each([
    ['', 'overview'],
    ['/', 'overview'],
    ['/tasks', 'tasks'],
    ['/gantt', 'gantt'],
    ['/timeline', 'timeline'],
    ['/members', 'members'],
    ['/settings', 'settings'],
  ])('maps %s to %s', (suffix, view) => {
    at(suffix);
    expect(renderHook(() => useTopBarBreadcrumb()).result.current?.view).toBe(view);
  });
});
