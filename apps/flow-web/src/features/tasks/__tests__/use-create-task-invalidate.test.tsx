/**
 * Verify that useCreateTask broadcasts invalidation to both the per-project
 * task list AND the cross-workspace `me` infinite list. Both surfaces can
 * display a freshly created task, so a missing invalidate would leave the
 * UI showing stale data until manual refresh.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { tasksKeys, useCreateTask } from '../api';

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch exposes HTTP verbs uppercased.
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
  it('invalidates the per-project list and the me list on success', async () => {
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
        queryKey: [...tasksKeys.all, 'list', 'proj-1'],
      });
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: tasksKeys.myInfinite(),
      });
    });
  });
});
