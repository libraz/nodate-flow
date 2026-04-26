/**
 * Cursor-paginated notifications hook coverage.
 *
 * Verifies:
 *   1. `useNotificationsInfiniteQuery` flat-maps `data.pages` correctly
 *      across 3 pages of 10 items, with `nextCursor` advancing each time.
 *   2. `notificationKeys` factory keeps the infinite key under the shared
 *      `[...all, 'list']` prefix so the existing W5 mutation policy
 *      refreshes it.
 *
 * The fetch surface is mocked at the global `fetch` boundary because the
 * notifications module talks to the API via raw `fetch` (the SDK does
 * not yet expose these endpoints with the workspace-scoped path the
 * dropdown uses).
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { type NotificationItem, notificationKeys, useNotificationsInfiniteQuery } from '../api';

/* ── Fixture helpers ──────────────────────────────────────── */

function aNotification(id: string, overrides: Partial<NotificationItem> = {}): NotificationItem {
  return {
    id,
    workspaceId: 'ws-1',
    actorId: null,
    actorDisplayName: null,
    eventType: 'task.created',
    resourceType: 'task',
    resourceId: 'task-1',
    title: `Notification ${id}`,
    body: null,
    severity: 'normal',
    channel: 'in_app',
    readAt: null,
    deliveredAt: null,
    createdAt: 1_700_000_000,
    total: 30,
    ...overrides,
  };
}

/** Build 3 pages of 10 notifications, advancing `nextCursor` per page. */
function buildPages(): {
  pageOne: { notifications: NotificationItem[]; nextCursor: string | null };
  pageTwo: { notifications: NotificationItem[]; nextCursor: string | null };
  pageThree: { notifications: NotificationItem[]; nextCursor: string | null };
} {
  const make = (start: number): NotificationItem[] =>
    Array.from({ length: 10 }, (_, i) => aNotification(`n-${start + i}`));
  return {
    pageOne: { notifications: make(0), nextCursor: 'cursor-2' },
    pageTwo: { notifications: make(10), nextCursor: 'cursor-3' },
    pageThree: { notifications: make(20), nextCursor: null },
  };
}

/* ── Provider wrapper ────────────────────────────────────── */

function makeWrapper(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>{children}</Suspense>
      </QueryClientProvider>
    );
  };
}

function buildClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
}

/* ── Mock global fetch ───────────────────────────────────── */

const fetchMock = vi.fn();
const originalFetch = globalThis.fetch;

beforeEach(() => {
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  fetchMock.mockReset();
});

afterEach(() => {
  globalThis.fetch = originalFetch;
});

/** Wrap a payload as a Response-like object the api.ts `fetchJson` consumes. */
function ok(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: async () => body,
  } as unknown as Response;
}

/* ── Tests ───────────────────────────────────────────────── */

describe('useNotificationsInfiniteQuery', () => {
  it('flat-maps 3 pages of 10 items into 30 items as the cursor advances', async () => {
    const { pageOne, pageTwo, pageThree } = buildPages();
    fetchMock
      .mockResolvedValueOnce(ok(pageOne))
      .mockResolvedValueOnce(ok(pageTwo))
      .mockResolvedValueOnce(ok(pageThree));

    const client = buildClient();
    const { result } = renderHook(() => useNotificationsInfiniteQuery(), {
      wrapper: makeWrapper(client),
    });

    // First page resolves under suspense.
    await waitFor(() => {
      expect(result.current.data).toBeDefined();
    });
    expect(result.current.data.pages).toHaveLength(1);
    expect(result.current.data.pages[0]?.notifications).toHaveLength(10);
    expect(result.current.hasNextPage).toBe(true);

    // First call must NOT carry a cursor query param.
    const firstUrl = String(fetchMock.mock.calls[0]?.[0] ?? '');
    expect(firstUrl).toContain('limit=20');
    expect(firstUrl).not.toContain('cursor=');

    // Page 2.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(2);
    });
    const secondUrl = String(fetchMock.mock.calls[1]?.[0] ?? '');
    expect(secondUrl).toContain('cursor=cursor-2');

    // Page 3.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(3);
    });
    const thirdUrl = String(fetchMock.mock.calls[2]?.[0] ?? '');
    expect(thirdUrl).toContain('cursor=cursor-3');

    // Final state: 30 items flat-mapped, no further pages.
    const flat = result.current.data.pages.flatMap((p) => p.notifications);
    expect(flat).toHaveLength(30);
    expect(flat[0]?.id).toBe('n-0');
    expect(flat.at(-1)?.id).toBe('n-29');
    expect(result.current.hasNextPage).toBe(false);
  });
});

describe('notificationKeys factory', () => {
  it('infinite key is under the shared list prefix', () => {
    const all = notificationKeys.all;
    const list = notificationKeys.list();
    const infinite = notificationKeys.infinite();
    expect(list.slice(0, all.length)).toEqual([...all]);
    expect(infinite.slice(0, list.length)).toEqual([...list]);
  });

  it('list-prefix invalidation matches both list and infinite keys', () => {
    const prefix = [...notificationKeys.all, 'list'];
    const matches = (target: readonly unknown[]): boolean =>
      prefix.every((v, i) => v === target[i]);
    expect(matches(notificationKeys.list())).toBe(true);
    expect(matches(notificationKeys.infinite())).toBe(true);
    expect(matches(notificationKeys.unreadCount())).toBe(false);
  });
});
