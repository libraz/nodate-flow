/**
 * useReorderTasks cache update coverage.
 *
 * Regression: the list view's drag-to-reorder consumes the infinite
 * `useTasksInfiniteQuery`, whose cache value is `InfiniteData<TasksPage>`.
 * The previous implementation of `onMutate` treated the cached value as a
 * flat `TaskListItem[]` and called `value.map(...)`, which threw a
 * `TypeError` and rejected `mutateAsync` before the network request was
 * ever dispatched.
 *
 * These tests prime the cache with `InfiniteData<TasksPage>` and assert:
 *   1. `mutateAsync` resolves without throwing,
 *   2. the optimistic cache update walks `pages[].tasks` correctly and
 *      reflects the new sort order,
 *   3. the SDK `/tasks/reorder` POST is dispatched exactly once,
 *   4. on a server error the rollback restores the original snapshot.
 */

import { type InfiniteData, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    POST: sdkMocks.post,
  },
}));

import { type TaskListItem, type TasksPage, tasksKeys, useReorderTasks } from '../api';

function aTask(id: string, sortWeight: number): TaskListItem {
  return {
    id,
    projectId: 'prj-1',
    workspaceId: 'ws-1',
    title: `Task ${id}`,
    derivedState: 'open',
    priority: 0,
    primaryAssigneeId: null,
    assigneeCount: 0,
    sortWeight,
    createdAt: 1_700_000_000,
    updatedAt: 1_700_000_000,
  } as unknown as TaskListItem;
}

function buildClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
}

function makeWrapper(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

function primeInfinite(client: QueryClient, tasks: TaskListItem[]): readonly unknown[] {
  const key = tasksKeys.infinite('prj-1');
  const data: InfiniteData<TasksPage> = {
    pages: [{ tasks, nextCursor: null, total: tasks.length, offset: 0 }],
    pageParams: [0],
  };
  client.setQueryData(key, data);
  return key;
}

beforeEach(() => {
  sdkMocks.post.mockReset();
});

describe('useReorderTasks with InfiniteData cache', () => {
  it('does not throw inside onMutate and reorders the cached infinite list', async () => {
    sdkMocks.post.mockResolvedValueOnce({ data: undefined, error: null });

    const client = buildClient();
    const initial = [aTask('a', 1000), aTask('b', 2000), aTask('c', 3000)];
    const key = primeInfinite(client, initial);

    const { result } = renderHook(() => useReorderTasks(), {
      wrapper: makeWrapper(client),
    });

    // Move 'c' to the front.
    await expect(
      result.current.mutateAsync({
        projectId: 'prj-1',
        items: [
          { id: 'c', sortWeight: 1000 },
          { id: 'a', sortWeight: 2000 },
          { id: 'b', sortWeight: 3000 },
        ],
      }),
    ).resolves.toBeUndefined();

    // SDK was called exactly once with the right payload.
    expect(sdkMocks.post).toHaveBeenCalledTimes(1);
    const call = sdkMocks.post.mock.calls[0];
    expect(call?.[0]).toBe('/tasks/reorder');
    expect(call?.[1]?.body?.projectId).toBe('prj-1');
    expect(call?.[1]?.body?.items).toHaveLength(3);

    // Cache reflects the new order under InfiniteData.pages[].tasks.
    const updated = client.getQueryData<InfiniteData<TasksPage>>(key);
    expect(updated).toBeDefined();
    expect(updated?.pages).toHaveLength(1);
    const order = (updated?.pages[0]?.tasks ?? []).map((t) => t.id);
    expect(order).toEqual(['c', 'a', 'b']);
  });

  it('rolls the cache back to the original order on server error', async () => {
    sdkMocks.post.mockResolvedValueOnce({
      data: undefined,
      error: { type: 'about:blank', title: 'Internal Server Error', status: 500 },
    });

    const client = buildClient();
    const initial = [aTask('a', 1000), aTask('b', 2000), aTask('c', 3000)];
    const key = primeInfinite(client, initial);

    const { result } = renderHook(() => useReorderTasks(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        projectId: 'prj-1',
        items: [
          { id: 'c', sortWeight: 1000 },
          { id: 'a', sortWeight: 2000 },
          { id: 'b', sortWeight: 3000 },
        ],
      }),
    ).rejects.toBeDefined();

    // The fetch was still attempted exactly once.
    expect(sdkMocks.post).toHaveBeenCalledTimes(1);

    // Cache rolled back to the pre-mutation order with original sort weights.
    const restored = client.getQueryData<InfiniteData<TasksPage>>(key);
    expect(restored).toBeDefined();
    const order = (restored?.pages[0]?.tasks ?? []).map((t) => t.id);
    expect(order).toEqual(['a', 'b', 'c']);
    const weights = (restored?.pages[0]?.tasks ?? []).map((t) => t.sortWeight);
    expect(weights).toEqual([1000, 2000, 3000]);
  });

  it('also handles the legacy flat TaskListItem[] cache shape', async () => {
    sdkMocks.post.mockResolvedValueOnce({ data: undefined, error: null });

    const client = buildClient();
    const flatKey = tasksKeys.list('prj-1');
    client.setQueryData<TaskListItem[]>(flatKey, [
      aTask('a', 1000),
      aTask('b', 2000),
      aTask('c', 3000),
    ]);

    const { result } = renderHook(() => useReorderTasks(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        projectId: 'prj-1',
        items: [
          { id: 'b', sortWeight: 1000 },
          { id: 'a', sortWeight: 2000 },
          { id: 'c', sortWeight: 3000 },
        ],
      }),
    ).resolves.toBeUndefined();

    const flat = client.getQueryData<TaskListItem[]>(flatKey);
    expect(flat?.map((t) => t.id)).toEqual(['b', 'a', 'c']);
  });
});
