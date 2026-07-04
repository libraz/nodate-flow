/**
 * useTransitionTask optimistic cache update coverage.
 *
 * Regression: the state-transition mutation's `onMutate` iterated every
 * cache entry under the `[...tasksKeys.all, 'list']` prefix and called
 * `value.map(...)` unconditionally. When a Board/List session has primed
 * the cursor-paginated cache (`useTasksInfiniteQuery`), the cached value
 * is `InfiniteData<TasksPage>` (`{ pages, pageParams }`), not a flat
 * `TaskListItem[]`. `.map` then threw a `TypeError`, TanStack marked the
 * (actually-successful) mutation as failed, surfaced a spurious error
 * toast, and never applied the optimistic move.
 *
 * These tests prime the cache with BOTH shapes and assert:
 *   1. `mutateAsync` resolves without throwing for either shape,
 *   2. the optimistic update flips `derivedState` in a flat list and in
 *      every `pages[].tasks` entry of an InfiniteData cache,
 *   3. on a server error the rollback restores the original snapshot for
 *      both shapes (no crash in the rollback path either).
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

import {
  type Task,
  type TaskDerivedState,
  type TaskListItem,
  type TasksPage,
  tasksKeys,
  useTransitionTask,
} from '../api';

function aTask(id: string, derivedState: TaskDerivedState): TaskListItem {
  return {
    id,
    projectId: 'prj-1',
    workspaceId: 'ws-1',
    title: `Task ${id}`,
    derivedState,
    priority: 0,
    primaryAssigneeId: null,
    assigneeCount: 0,
    sortWeight: 1000,
    createdAt: 1_700_000_000,
    updatedAt: 1_700_000_000,
  } as unknown as TaskListItem;
}

function aServerTask(id: string, derivedState: TaskDerivedState): Task {
  return {
    id,
    projectId: 'prj-1',
    workspaceId: 'ws-1',
    title: `Task ${id}`,
    derivedState,
  } as unknown as Task;
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
    pages: [{ tasks, nextCursor: null, total: tasks.length, offset: 0, nextOffset: tasks.length }],
    pageParams: [0],
  };
  client.setQueryData(key, data);
  return key;
}

beforeEach(() => {
  sdkMocks.post.mockReset();
});

describe('useTransitionTask optimistic update — plain-array cache', () => {
  it('does not throw and flips derivedState in the flat list', async () => {
    sdkMocks.post.mockResolvedValueOnce({
      data: aServerTask('a', 'waiting'),
      error: null,
    });

    const client = buildClient();
    const flatKey = tasksKeys.list('prj-1');
    client.setQueryData<TaskListItem[]>(flatKey, [aTask('a', 'open'), aTask('b', 'open')]);

    const { result } = renderHook(() => useTransitionTask(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        id: 'a',
        transition: 'start',
        projectId: 'prj-1',
        optimisticState: 'waiting',
      }),
    ).resolves.toBeDefined();

    expect(sdkMocks.post).toHaveBeenCalledTimes(1);
    expect(sdkMocks.post.mock.calls[0]?.[0]).toBe('/tasks/{id}/transitions');

    const flat = client.getQueryData<TaskListItem[]>(flatKey);
    expect(flat?.find((t) => t.id === 'a')?.derivedState).toBe('waiting');
    // Untouched siblings keep their state.
    expect(flat?.find((t) => t.id === 'b')?.derivedState).toBe('open');
  });

  it('rolls the flat list back to the original state on server error', async () => {
    sdkMocks.post.mockResolvedValueOnce({
      data: undefined,
      error: { type: 'about:blank', title: 'Internal Server Error', status: 500 },
    });

    const client = buildClient();
    const flatKey = tasksKeys.list('prj-1');
    client.setQueryData<TaskListItem[]>(flatKey, [aTask('a', 'open')]);

    const { result } = renderHook(() => useTransitionTask(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        id: 'a',
        transition: 'start',
        projectId: 'prj-1',
        optimisticState: 'waiting',
      }),
    ).rejects.toBeDefined();

    const flat = client.getQueryData<TaskListItem[]>(flatKey);
    expect(flat?.find((t) => t.id === 'a')?.derivedState).toBe('open');
  });
});

describe('useTransitionTask optimistic update — InfiniteData cache', () => {
  it('does not throw and flips derivedState inside pages[].tasks', async () => {
    sdkMocks.post.mockResolvedValueOnce({
      data: aServerTask('a', 'waiting'),
      error: null,
    });

    const client = buildClient();
    const key = primeInfinite(client, [aTask('a', 'open'), aTask('b', 'open')]);

    const { result } = renderHook(() => useTransitionTask(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        id: 'a',
        transition: 'start',
        projectId: 'prj-1',
        optimisticState: 'waiting',
      }),
    ).resolves.toBeDefined();

    expect(sdkMocks.post).toHaveBeenCalledTimes(1);

    const updated = client.getQueryData<InfiniteData<TasksPage>>(key);
    expect(updated).toBeDefined();
    const tasks = updated?.pages[0]?.tasks ?? [];
    expect(tasks.find((t) => t.id === 'a')?.derivedState).toBe('waiting');
    expect(tasks.find((t) => t.id === 'b')?.derivedState).toBe('open');
  });

  it('rolls the infinite cache back to the original state on server error', async () => {
    sdkMocks.post.mockResolvedValueOnce({
      data: undefined,
      error: { type: 'about:blank', title: 'Internal Server Error', status: 500 },
    });

    const client = buildClient();
    const key = primeInfinite(client, [aTask('a', 'open')]);

    const { result } = renderHook(() => useTransitionTask(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        id: 'a',
        transition: 'start',
        projectId: 'prj-1',
        optimisticState: 'waiting',
      }),
    ).rejects.toBeDefined();

    const restored = client.getQueryData<InfiniteData<TasksPage>>(key);
    expect(restored).toBeDefined();
    expect(restored?.pages[0]?.tasks[0]?.derivedState).toBe('open');
  });

  it('updates both flat and infinite caches in the same project simultaneously', async () => {
    // Repro path from the audit: List view primes the flat/infinite cache,
    // Board view of the same project caches an infinite entry, then a card
    // move triggers the transition. Both must update without a TypeError.
    sdkMocks.post.mockResolvedValueOnce({
      data: aServerTask('a', 'waiting'),
      error: null,
    });

    const client = buildClient();
    const flatKey = tasksKeys.list('prj-1');
    client.setQueryData<TaskListItem[]>(flatKey, [aTask('a', 'open')]);
    const infiniteKey = primeInfinite(client, [aTask('a', 'open')]);

    const { result } = renderHook(() => useTransitionTask(), {
      wrapper: makeWrapper(client),
    });

    await expect(
      result.current.mutateAsync({
        id: 'a',
        transition: 'start',
        projectId: 'prj-1',
        optimisticState: 'waiting',
      }),
    ).resolves.toBeDefined();

    const flat = client.getQueryData<TaskListItem[]>(flatKey);
    expect(flat?.[0]?.derivedState).toBe('waiting');
    const infinite = client.getQueryData<InfiniteData<TasksPage>>(infiniteKey);
    expect(infinite?.pages[0]?.tasks[0]?.derivedState).toBe('waiting');
  });
});
