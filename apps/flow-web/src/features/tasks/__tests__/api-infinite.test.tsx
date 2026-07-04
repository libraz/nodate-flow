/**
 * Infinite project tasks hook coverage.
 *
 * Verifies:
 *   1. `useTasksInfiniteQuery` flat-maps `data.pages` correctly across
 *      3 pages, with OFFSET advancing each time, and the offset is
 *      threaded through `pageParam` (NOT into the query
 *      key).
 *   2. `tasksKeys.infinite` lives under the shared `[...all, 'list']`
 *      prefix so the W5 mutation invalidation policy refreshes it.
 *
 * The SDK is mocked at the `sdk.GET` boundary because the tasks list
 * goes through the typed openapi-fetch client.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    GET: sdkMocks.get,
  },
}));

import { type TaskListItem, tasksKeys, useTasksInfiniteQuery } from '../api';

/* ── Fixture helpers ──────────────────────────────────────── */

function aTask(id: string): TaskListItem {
  return {
    id,
    projectId: 'prj-1',
    workspaceId: 'ws-1',
    title: `Task ${id}`,
    derivedState: 'open',
    priority: 0,
    primaryAssigneeId: null,
    assigneeCount: 0,
    sortWeight: 0,
    createdAt: 1_700_000_000,
    updatedAt: 1_700_000_000,
  } as unknown as TaskListItem;
}

interface FakePage {
  tasks: TaskListItem[];
  total: number;
}

function buildPages(): { pageOne: FakePage; pageTwo: FakePage; pageThree: FakePage } {
  const make = (start: number, count: number): TaskListItem[] =>
    Array.from({ length: count }, (_, i) => aTask(`t-${start + i}`));
  return {
    pageOne: { tasks: make(0, 100), total: 250 },
    pageTwo: { tasks: make(100, 100), total: 250 },
    pageThree: { tasks: make(200, 50), total: 250 },
  };
}

/* ── Provider wrapper ────────────────────────────────────── */

function buildClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
}

function makeWrapper(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>{children}</Suspense>
      </QueryClientProvider>
    );
  };
}

beforeEach(() => {
  sdkMocks.get.mockReset();
});

/* ── Tests ───────────────────────────────────────────────── */

describe('useTasksInfiniteQuery', () => {
  it('flat-maps 3 pages of tasks across offset advances', async () => {
    const { pageOne, pageTwo, pageThree } = buildPages();
    sdkMocks.get
      .mockResolvedValueOnce({ data: pageOne, error: null })
      .mockResolvedValueOnce({ data: pageTwo, error: null })
      .mockResolvedValueOnce({ data: pageThree, error: null });

    const client = buildClient();
    const { result } = renderHook(() => useTasksInfiniteQuery('prj-1'), {
      wrapper: makeWrapper(client),
    });

    // Page 1.
    await waitFor(() => {
      expect(result.current.data).toBeDefined();
    });
    expect(result.current.data.pages).toHaveLength(1);
    expect(result.current.data.pages[0]?.tasks).toHaveLength(100);
    expect(result.current.hasNextPage).toBe(true);

    // First call starts at offset 0 and must NOT carry a cursor; the
    // project list needs OFFSET to preserve sort_weight ordering.
    const firstCall = sdkMocks.get.mock.calls[0];
    expect(firstCall?.[0]).toBe('/tasks');
    const firstQuery = firstCall?.[1]?.params?.query as Record<string, unknown> | undefined;
    expect(firstQuery?.projectId).toBe('prj-1');
    expect(firstQuery?.cursor).toBeUndefined();
    expect(firstQuery?.limit).toBe(100);
    expect(firstQuery?.offset).toBe(0);

    // Page 2 — offset advances by page size.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(2);
    });
    const secondQuery = sdkMocks.get.mock.calls[1]?.[1]?.params?.query as
      | Record<string, unknown>
      | undefined;
    expect(secondQuery?.cursor).toBeUndefined();
    expect(secondQuery?.offset).toBe(100);

    // Page 3 — offset advances again.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(3);
    });
    const thirdQuery = sdkMocks.get.mock.calls[2]?.[1]?.params?.query as
      | Record<string, unknown>
      | undefined;
    expect(thirdQuery?.cursor).toBeUndefined();
    expect(thirdQuery?.offset).toBe(200);

    // Final state.
    const flat = result.current.data.pages.flatMap((p) => p.tasks);
    expect(flat).toHaveLength(250);
    expect(flat[0]?.id).toBe('t-0');
    expect(flat.at(-1)?.id).toBe('t-249');
    expect(result.current.hasNextPage).toBe(false);
  });

  it('keeps pagination state out of the queryKey', () => {
    // The offset must NOT be folded into the queryKey explicitly —
    // TanStack threads it via `pageParam` so cache layout stays stable
    // across page advances. The key shape must therefore be independent
    // of any page position.
    const key = tasksKeys.infinite('prj-1');
    const flat = JSON.stringify(key);
    expect(flat).not.toContain('cursor');
    expect(flat).not.toContain('offset');
  });
});

describe('tasksKeys.infinite — invalidation prefix', () => {
  it('infinite key sits under the shared [...all, "list"] prefix', () => {
    const prefix = [...tasksKeys.all, 'list'];
    const infinite = tasksKeys.infinite('prj-1');
    const matches = (target: readonly unknown[]): boolean =>
      prefix.every((v, i) => v === target[i]);
    expect(matches(infinite)).toBe(true);
  });

  it('detail keys are NOT matched by the list prefix', () => {
    const prefix = [...tasksKeys.all, 'list'];
    const detail = tasksKeys.detail('t1');
    const matches = (target: readonly unknown[]): boolean =>
      prefix.every((v, i) => v === target[i]);
    expect(matches(detail)).toBe(false);
  });
});
