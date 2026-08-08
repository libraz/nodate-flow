/**
 * Infinite project tasks hook coverage.
 *
 * Verifies:
 *   1. `useTasksInfiniteQuery` flat-maps `data.pages` correctly across
 *      3 pages, with OFFSET advancing each time, and the offset is
 *      threaded through `pageParam` (NOT into the query
 *      key).
 *   1b. every filter, priority included, travels as a query parameter
 *      and one page fetch costs exactly one request.
 *   2. `tasksKeys.infinite` lives under the shared `[...all, 'list']`
 *      prefix so the W5 mutation invalidation policy refreshes it.
 *
 * Intercepted at the fetch boundary with MSW rather than by mocking
 * `lib/sdk`. The distinction matters for exactly the assertions this
 * file makes: a mocked `sdk.GET` can only report the arguments the hook
 * passed *in*, so it agrees with the hook by construction and cannot
 * see how the SDK serialized them. Reading `request.url` instead means
 * these tests fail if the generated client starts putting `priority` on
 * the wire as repeated parameters, renames a field, or drops one —
 * drift the previous version of this file would have reported as green.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { HttpResponse, http } from 'msw';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { describe, expect, it } from 'vitest';

import { API_URL, server, useMockApi } from '../../../../tests/msw/server';
import { type TaskListItem, tasksKeys, useTasksInfiniteQuery } from '../api';

useMockApi();

/* ── Fixture helpers ──────────────────────────────────────── */

function aTask(id: string, priority = 0): TaskListItem {
  return {
    id,
    projectId: 'prj-1',
    workspaceId: 'ws-1',
    title: `Task ${id}`,
    derivedState: 'open',
    priority,
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

/**
 * Registers a `GET /tasks` handler and records the URL of every call
 * it answers, so assertions run against what actually reached the wire.
 */
function captureTaskRequests(respond: (call: number) => FakePage): URL[] {
  const seen: URL[] = [];
  let call = 0;
  server.use(
    http.get(`${API_URL}/tasks`, ({ request }) => {
      seen.push(new URL(request.url));
      const page = respond(call);
      call += 1;
      return HttpResponse.json(page);
    }),
  );
  return seen;
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

/* ── Tests ───────────────────────────────────────────────── */

describe('useTasksInfiniteQuery', () => {
  it('flat-maps 3 pages of tasks across offset advances', async () => {
    const { pageOne, pageTwo, pageThree } = buildPages();
    const pages = [pageOne, pageTwo, pageThree];
    const seen = captureTaskRequests((call) => pages[call] ?? { tasks: [], total: 250 });

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
    const first = seen[0];
    expect(first?.pathname).toBe('/tasks');
    expect(first?.searchParams.get('projectId')).toBe('prj-1');
    expect(first?.searchParams.has('cursor')).toBe(false);
    expect(first?.searchParams.get('limit')).toBe('100');
    expect(first?.searchParams.get('offset')).toBe('0');

    // Page 2 — offset advances by page size.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(2);
    });
    expect(seen[1]?.searchParams.has('cursor')).toBe(false);
    expect(seen[1]?.searchParams.get('offset')).toBe('100');

    // Page 3 — offset advances again.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(3);
    });
    expect(seen[2]?.searchParams.has('cursor')).toBe(false);
    expect(seen[2]?.searchParams.get('offset')).toBe('200');

    // Final state.
    const flat = result.current.data.pages.flatMap((p) => p.tasks);
    expect(flat).toHaveLength(250);
    expect(flat[0]?.id).toBe('t-0');
    expect(flat.at(-1)?.id).toBe('t-249');
    expect(result.current.hasNextPage).toBe(false);
  });

  it('sends the priority filter to the server and issues one request per page', async () => {
    // The hook used to fetch page after page inside a single queryFn
    // until a client-side priority filter produced a row. Selecting a
    // rare priority in a large project therefore cost one blocking
    // request per empty page. Priority is a server parameter now, so a
    // page fetch is exactly one request whatever the filter matches.
    const page: FakePage = {
      tasks: [aTask('urgent-1', 4), aTask('urgent-2', 4)],
      total: 2,
    };
    const seen = captureTaskRequests(() => page);

    const client = buildClient();
    const { result } = renderHook(() => useTasksInfiniteQuery('prj-1', { priority: [4, 3] }), {
      wrapper: makeWrapper(client),
    });

    await waitFor(() => {
      expect(result.current.data).toBeDefined();
    });

    expect(seen).toHaveLength(1);
    expect(seen[0]?.searchParams.get('offset')).toBe('0');

    // The multi-value filter reaches the server as one comma-joined
    // parameter, not as repeated keys — the form the API parses. This
    // is the SDK client's serializer doing its job; asserting it on the
    // real URL is the whole reason this test goes through MSW.
    expect(seen[0]?.searchParams.getAll('priority')).toEqual(['4,3']);

    expect(result.current.data.pages[0]?.tasks.map((task) => task.id)).toEqual([
      'urgent-1',
      'urgent-2',
    ]);
  });

  it('does not chase further pages when the server returns an empty page', async () => {
    // An empty page is the server's answer for this offset, not a cue
    // to keep asking. The old loop read it as "keep going" and walked
    // the whole project one request at a time.
    const seen = captureTaskRequests(() => ({ tasks: [], total: 5000 }));

    const client = buildClient();
    const { result } = renderHook(() => useTasksInfiniteQuery('prj-1', { priority: [4] }), {
      wrapper: makeWrapper(client),
    });

    await waitFor(() => {
      expect(result.current.data).toBeDefined();
    });

    expect(seen).toHaveLength(1);
    expect(result.current.data.pages[0]?.tasks).toEqual([]);
  });

  it('omits the priority parameter when no priority filter is active', async () => {
    const seen = captureTaskRequests(() => ({ tasks: [aTask('t-0')], total: 1 }));

    const client = buildClient();
    const { result } = renderHook(() => useTasksInfiniteQuery('prj-1', { priority: [] }), {
      wrapper: makeWrapper(client),
    });

    await waitFor(() => {
      expect(result.current.data).toBeDefined();
    });
    expect(seen[0]?.searchParams.has('priority')).toBe(false);
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
