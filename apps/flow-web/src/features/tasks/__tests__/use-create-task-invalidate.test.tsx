/**
 * Verify that useCreateTask broadcasts invalidation to the shared list prefix.
 * Project, filtered, infinite, and cross-workspace `me` lists all live under
 * this prefix, so a narrower invalidate can leave one surface stale.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { tasksKeys, useCreateTask } from '../api';

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    POST: vi.fn(async () => ({
      // Minimal payload — the create hook only reads it to resolve the mutation,
      // not to populate downstream caches, so a structural cast is safe here.
      data: {
        id: 'task-public-id',
        projectId: 'proj-1',
      } as unknown,
      error: undefined,
    })),
  },
}));

function makeWrapper(qc: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe('useCreateTask invalidation', () => {
  it('invalidates the shared task-list prefix on success', async () => {
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');

    const wrapper = makeWrapper(qc);
    const { result } = renderHook(() => useCreateTask(), { wrapper });

    await result.current.mutateAsync({
      projectId: 'proj-1',
      title: 'New task',
      priority: 2,
      visibility: 'public',
    });

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: [...tasksKeys.all, 'list'],
      });
    });
  });
});
