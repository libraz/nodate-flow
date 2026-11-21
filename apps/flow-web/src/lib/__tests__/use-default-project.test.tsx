import { QueryClient } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { projectsKeys } from '../../features/projects/api';

const mocks = vi.hoisted(() => ({ pathname: '/today' }));

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>();
  return {
    ...actual,
    useRouterState: ({ select }: { select: (s: { location: { pathname: string } }) => unknown }) =>
      select({ location: { pathname: mocks.pathname } }),
  };
});
vi.mock('../use-current-workspace', () => ({ useActiveWorkspaceId: () => 'ws-1' }));

import { QueryClientProvider } from '@tanstack/react-query';
import { useDefaultProjectId } from '../use-default-project';

function resolveWith(projects: unknown[]): ReturnType<typeof useDefaultProjectId> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
  });
  client.setQueryData(projectsKeys.list('ws-1'), projects);
  const { result } = renderHook(() => useDefaultProjectId(), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  return result.current;
}

beforeEach(() => {
  mocks.pathname = '/today';
});

describe('useDefaultProjectId', () => {
  it('skips archived projects when picking the fallback', () => {
    const r = resolveWith([
      { id: 'prj-old', isArchived: true },
      { id: 'prj-live', isArchived: false },
    ]);
    expect(r).toEqual({ projectId: 'prj-live', workspaceId: 'ws-1' });
  });

  it('resolves to no project when every project is archived', () => {
    const r = resolveWith([{ id: 'prj-old', isArchived: true }]);
    expect(r.projectId).toBeNull();
  });
});
