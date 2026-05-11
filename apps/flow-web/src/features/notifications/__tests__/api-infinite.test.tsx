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
 * The fetch surface is mocked at the global `fetch` boundary; the
 * notifications module talks to the API through `@nodate-flow/sdk`
 * (openapi-fetch). openapi-fetch destructures `globalThis.fetch` at
 * client-construction time, so the mock is installed BEFORE the
 * `../api` module (which imports the SDK client) is loaded — otherwise
 * the SDK keeps a reference to the real fetch and bypasses the mock.
 * That ordering is enforced by `vi.hoisted`, which runs before the
 * static imports below.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

const { fetchMock, originalFetch } = vi.hoisted(() => {
  const mock = vi.fn();
  const original = globalThis.fetch;
  globalThis.fetch = mock as unknown as typeof fetch;
  return { fetchMock: mock, originalFetch: original };
});

// Imports below run AFTER the hoisted fetch stub, so the SDK module
// (loaded transitively via `../api`) captures the mock as its base
// fetch.
import { authStore } from '../../auth/auth-store';
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
  pageOne: { notifications: NotificationItem[]; nextCursor: string | null; total: number };
  pageTwo: { notifications: NotificationItem[]; nextCursor: string | null; total: number };
  pageThree: { notifications: NotificationItem[]; nextCursor: string | null; total: number };
} {
  const make = (start: number): NotificationItem[] =>
    Array.from({ length: 10 }, (_, i) => aNotification(`n-${start + i}`));
  return {
    pageOne: { notifications: make(0), nextCursor: 'cursor-2', total: 30 },
    pageTwo: { notifications: make(10), nextCursor: 'cursor-3', total: 30 },
    pageThree: { notifications: make(20), nextCursor: null, total: 30 },
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

/**
 * Seed the SDK auth store with a long-lived synthetic JWT so the
 * SDK's request middleware skips its proactive `/auth/refresh` round
 * trip and lets the mocked fetch see only the notifications calls
 * the test cares about. The token's `exp` is hours in the future to
 * sidestep the `isExpiringSoon` check.
 */
function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

beforeAll(() => {
  seedAuthToken();
});

beforeEach(() => {
  // Re-seed before each test in case a previous test (or SDK middleware)
  // cleared the session via `clearSession()`.
  seedAuthToken();
  fetchMock.mockReset();
});

afterAll(() => {
  // Restore on test-suite teardown to keep other test files unaffected.
  globalThis.fetch = originalFetch;
});

/**
 * Wrap a payload as a real `Response` so openapi-fetch can read its
 * headers / body without crashing on a partial mock.
 */
function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
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

    // openapi-fetch invokes `fetch` with a `Request` instance, not a URL
    // string — pull `.url` (or fall back to a stringified call arg) so
    // these assertions cover both fetch input shapes.
    const callUrl = (idx: number): string => {
      const arg = fetchMock.mock.calls[idx]?.[0];
      if (arg instanceof Request) return arg.url;
      return String(arg ?? '');
    };

    // First call must NOT carry a cursor query param.
    const firstUrl = callUrl(0);
    expect(firstUrl).toContain('limit=20');
    expect(firstUrl).not.toContain('cursor=');

    // Page 2.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(2);
    });
    expect(callUrl(1)).toContain('cursor=cursor-2');

    // Page 3.
    await result.current.fetchNextPage();
    await waitFor(() => {
      expect(result.current.data.pages).toHaveLength(3);
    });
    expect(callUrl(2)).toContain('cursor=cursor-3');

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
