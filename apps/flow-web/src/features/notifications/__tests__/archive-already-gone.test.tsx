/**
 * Archive mutation coverage for a refusal that means "already gone".
 *
 * The archive endpoint answers `WS.NOTIFICATION.NOT_FOUND` when it
 * archived nothing, which includes the ordinary race of archiving a
 * notification that is already archived. `useArchiveNotification` treats
 * that one code as success: the optimistic removal stands and no error
 * reaches the caller.
 *
 * Both directions are pinned, because an implementation that swallowed
 * every failure would satisfy the first half alone:
 *   1. `WS.NOTIFICATION.NOT_FOUND` -> mutation resolves, item stays removed.
 *   2. `WS.WORKSPACE.ACCESS_DENIED` -> mutation rejects, caches roll back.
 *
 * The fetch surface is mocked at the global `fetch` boundary. openapi-fetch
 * destructures `globalThis.fetch` at client-construction time, so the mock
 * is installed BEFORE `../api` (which imports the SDK client) is loaded —
 * enforced by `vi.hoisted`, which runs before the static imports below.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

const { fetchMock, originalFetch } = vi.hoisted(() => {
  const mock = vi.fn();
  const original = globalThis.fetch;
  globalThis.fetch = mock as unknown as typeof fetch;
  return { fetchMock: mock, originalFetch: original };
});

// Imports below run AFTER the hoisted fetch stub, so the SDK module
// (loaded transitively via `../api`) captures the mock as its base fetch.
import { authStore } from '../../auth/auth-store';
import {
  type NotificationItem,
  notificationKeys,
  useArchiveNotification,
  useMarkNotificationRead,
} from '../api';

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
    total: 2,
    ...overrides,
  };
}

function makeWrapper(client: QueryClient): (props: { children: ReactNode }) => ReactElement {
  return function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
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
 * Seed both notification caches with two unread rows and an unread count,
 * so a rollback is observable as the rows coming back.
 */
function seedCaches(client: QueryClient): void {
  client.setQueryData(notificationKeys.list(), [aNotification('n-1'), aNotification('n-2')]);
  client.setQueryData(notificationKeys.infinite(), {
    pages: [{ notifications: [aNotification('n-1'), aNotification('n-2')], nextCursor: null }],
    pageParams: [undefined],
  });
  client.setQueryData(notificationKeys.unreadCount(), 2);
}

/**
 * Seed the SDK auth store with a long-lived synthetic JWT so the SDK's
 * request middleware skips its proactive `/auth/refresh` round trip and the
 * mocked fetch sees only the archive call the test cares about.
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
  seedAuthToken();
  fetchMock.mockReset();
});

afterAll(() => {
  globalThis.fetch = originalFetch;
});

/** Wrap a problem+json refusal as a real `Response`. */
function refusal(status: number, code: string, detail: string): Response {
  return new Response(JSON.stringify({ type: code, title: code, detail, status }), {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  });
}

/** Read the ids currently held by the single-page list cache. */
function listIds(client: QueryClient): string[] {
  return (client.getQueryData<NotificationItem[]>(notificationKeys.list()) ?? []).map((i) => i.id);
}

/** Read the ids currently held by the infinite cache, across all pages. */
function infiniteIds(client: QueryClient): string[] {
  const data = client.getQueryData<{ pages: { notifications: NotificationItem[] }[] }>(
    notificationKeys.infinite(),
  );
  return (data?.pages ?? []).flatMap((p) => p.notifications.map((i) => i.id));
}

/* ── Tests ───────────────────────────────────────────────── */

describe('useArchiveNotification', () => {
  it('resolves and keeps the row removed when the API answers WS.NOTIFICATION.NOT_FOUND', async () => {
    fetchMock.mockResolvedValue(
      refusal(404, 'WS.NOTIFICATION.NOT_FOUND', 'Notification not found'),
    );

    const client = buildClient();
    seedCaches(client);
    const { result } = renderHook(() => useArchiveNotification(), {
      wrapper: makeWrapper(client),
    });

    await result.current.mutateAsync('n-1');

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(result.current.error).toBeNull();
    expect(listIds(client)).toEqual(['n-2']);
    expect(infiniteIds(client)).toEqual(['n-2']);
  });

  it('rejects and rolls back for any other refusal', async () => {
    fetchMock.mockResolvedValue(
      refusal(403, 'WS.WORKSPACE.ACCESS_DENIED', 'Workspace access denied'),
    );

    const client = buildClient();
    seedCaches(client);
    const { result } = renderHook(() => useArchiveNotification(), {
      wrapper: makeWrapper(client),
    });

    await expect(result.current.mutateAsync('n-1')).rejects.toMatchObject({
      code: 'WS.WORKSPACE.ACCESS_DENIED',
    });

    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
    expect(listIds(client)).toEqual(['n-1', 'n-2']);
    expect(infiniteIds(client)).toEqual(['n-1', 'n-2']);
    expect(client.getQueryData<number>(notificationKeys.unreadCount())).toBe(2);
  });
});

describe('useMarkNotificationRead', () => {
  it('still rejects on WS.NOTIFICATION.NOT_FOUND — the allowance is archive-only', async () => {
    fetchMock.mockResolvedValue(
      refusal(404, 'WS.NOTIFICATION.NOT_FOUND', 'Notification not found'),
    );

    const client = buildClient();
    seedCaches(client);
    const { result } = renderHook(() => useMarkNotificationRead(), {
      wrapper: makeWrapper(client),
    });

    await expect(result.current.mutateAsync('n-1')).rejects.toMatchObject({
      code: 'WS.NOTIFICATION.NOT_FOUND',
    });
  });
});
