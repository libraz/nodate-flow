/**
 * Notifications feature — query key factory, types, and hooks for
 * notification list, unread count, mark-read, archive, and mark-all-read.
 *
 * Types are defined inline because the SDK may not yet include these
 * endpoints. API calls use raw fetch via the shared base URL and auth
 * store token.
 */

import {
  type UseMutationResult,
  type UseQueryResult,
  type UseSuspenseInfiniteQueryResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

/** Notification item returned by the API. Timestamps are unix seconds. */
export interface NotificationItem {
  id: string;
  workspaceId: string;
  actorId: string | null;
  actorDisplayName: string | null;
  eventType: string;
  resourceType: string;
  resourceId: string | null;
  title: string;
  body: string | null;
  severity: 'low' | 'normal' | 'high' | 'urgent';
  channel: string;
  readAt: number | null;
  deliveredAt: number | null;
  createdAt: number;
  total: number;
}

/** Unread count envelope from the API. */
interface UnreadCountResponse {
  unreadCount: number;
}

/**
 * Query key factory for the notifications feature.
 *
 * `list` is the legacy single-page suspense query (still used by tests and
 * any caller that wants the whole bundle synchronously). `infinite` is the
 * cursor-paginated key used by `useNotificationsInfiniteQuery` — TanStack
 * threads the cursor into the cache via `pageParam`, so it MUST NOT be
 * folded into the key explicitly.
 *
 * Both keys share the `[...notificationKeys.all, 'list']` prefix so a
 * mutation that broadcasts `invalidateQueries({ queryKey: [...all, 'list'] })`
 * refreshes both surfaces atomically.
 */
export const notificationKeys = {
  all: ['notifications'] as const,
  list: () => [...notificationKeys.all, 'list'] as const,
  infinite: () => [...notificationKeys.all, 'list', 'infinite'] as const,
  unreadCount: () => [...notificationKeys.all, 'unread-count'] as const,
};

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as NotificationApiError };

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    // Thread `res.status` into the error so the global QueryCache 401
    // handler in the shared SDK QueryClient can detect a terminal auth
    // failure on polling endpoints (notifications unread-count) and
    // stop the poll / bounce the user to /login.
    throw toApiError(body, `Request failed with status ${String(res.status)}`, res.status);
  }
  return (await res.json()) as T;
}

/** Page size requested per call against `GET /me/notifications`. */
const NOTIFICATIONS_PAGE_SIZE = 20;

/** Shape of one page returned by `GET /me/notifications`. */
export interface NotificationsPage {
  notifications: NotificationItem[];
  nextCursor: string | null;
}

/**
 * GET /me/notifications — suspense query for the notification list.
 *
 * `useSuspenseQuery` always rethrows on error by design (it has no
 * `throwOnError` opt-out), so containment for the notifications panel
 * lives at the call site: NotificationBell wraps the `<Suspense>`
 * hosting `<NotificationDropdown>` in a local `<ErrorBoundary
 * fallback={null}>`, which catches anything this query throws before
 * it can cascade to the route ErrorBoundary.
 *
 * Kept as the single-page surface for callers that want the full bundle
 * synchronously (tests, the inbox stream). New surfaces should prefer
 * {@link useNotificationsInfiniteQuery}.
 */
export function useNotificationsQuery(): UseSuspenseQueryResult<NotificationItem[]> {
  return useSuspenseQuery({
    queryKey: notificationKeys.list(),
    queryFn: async (): Promise<NotificationItem[]> => {
      const data = await fetchJson<{ notifications?: NotificationItem[] }>(
        `${apiBaseUrl}/me/notifications?limit=50`,
      );
      return data.notifications ?? [];
    },
  });
}

/**
 * GET /me/notifications — cursor-paginated infinite query.
 *
 * Pre-v1 contract: an empty `cursor` query string requests the first page;
 * the response carries `nextCursor: string | null`, where `null` signals
 * the end. We thread the cursor through `pageParam` so TanStack handles
 * cache layout — do NOT include the cursor in the queryKey.
 *
 * The dropdown / inbox panel render `data.pages.flatMap(p => p.notifications)`.
 * `mark-read`, `archive`, and `mark-all-read` mutations invalidate this key
 * via the shared `[...notificationKeys.all, 'list']` prefix.
 */
export function useNotificationsInfiniteQuery(): UseSuspenseInfiniteQueryResult<
  { pages: NotificationsPage[]; pageParams: readonly (string | undefined)[] },
  ApiError
> {
  return useSuspenseInfiniteQuery({
    queryKey: notificationKeys.infinite(),
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }): Promise<NotificationsPage> => {
      const params = new URLSearchParams();
      params.set('limit', String(NOTIFICATIONS_PAGE_SIZE));
      if (pageParam) params.set('cursor', pageParam);
      const data = await fetchJson<{
        notifications?: NotificationItem[];
        nextCursor?: string | null;
      }>(`${apiBaseUrl}/me/notifications?${params.toString()}`);
      return {
        notifications: data.notifications ?? [],
        nextCursor: data.nextCursor ?? null,
      };
    },
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  });
}

/**
 * GET /me/notifications/unread-count — non-suspense query for the
 * badge count. Polls every 30 seconds as a fallback for missed SSE
 * events.
 *
 * If the most recent fetch failed with a 401 (terminal auth error,
 * the SDK refresh middleware has already given up and the session
 * has been cleared) we stop polling — the global QueryCache handler
 * in the shared SDK QueryClient will bounce the user to /login, and
 * a dead badge firing once every 30 s against an unauthenticated API
 * is both noisy and pointless. `refetchInterval` accepts a function
 * that returns `false` to disable the interval.
 */
export function useUnreadCountQuery(): UseQueryResult<number> {
  return useQuery({
    queryKey: notificationKeys.unreadCount(),
    queryFn: async (): Promise<number> => {
      const data = await fetchJson<UnreadCountResponse>(
        `${apiBaseUrl}/me/notifications/unread-count`,
      );
      return data.unreadCount;
    },
    refetchInterval: (query): number | false => {
      const err = query.state.error;
      if (err instanceof ApiError && err.httpStatus === 401) return false;
      return 30_000;
    },
    // This badge is decorative; opt out of the SDK-wide `throwOnError: true`
    // default so a transient failure never cascades to the route
    // ErrorBoundary. The bell's local ErrorBoundary swallows it.
    throwOnError: false,
  });
}

/**
 * Shape of the cached infinite-query data, mirroring TanStack's
 * `InfiniteData<NotificationsPage, string | undefined>`.
 *
 * Declared locally so we can use it for the optimistic
 * `setQueryData` / rollback calls without importing the generic.
 */
interface InfiniteNotificationsCache {
  pages: NotificationsPage[];
  pageParams: readonly (string | undefined)[];
}

/**
 * Apply `mapper` to every notification across every page of the cached
 * infinite query and write the result back. Returns the previous value
 * (or undefined when no infinite cache exists yet) so the caller can
 * roll back on error.
 */
function patchInfiniteCache(
  qc: ReturnType<typeof useQueryClient>,
  mapper: (item: NotificationItem) => NotificationItem | null,
): InfiniteNotificationsCache | undefined {
  const prev = qc.getQueryData<InfiniteNotificationsCache>(notificationKeys.infinite());
  if (!prev) return undefined;
  const nextPages: NotificationsPage[] = prev.pages.map((page) => {
    const out: NotificationItem[] = [];
    for (const item of page.notifications) {
      const mapped = mapper(item);
      if (mapped !== null) out.push(mapped);
    }
    return { notifications: out, nextCursor: page.nextCursor };
  });
  qc.setQueryData<InfiniteNotificationsCache>(notificationKeys.infinite(), {
    pages: nextPages,
    pageParams: prev.pageParams,
  });
  return prev;
}

/** Restore the infinite cache snapshot captured by `patchInfiniteCache`. */
function restoreInfiniteCache(
  qc: ReturnType<typeof useQueryClient>,
  snapshot: InfiniteNotificationsCache | undefined,
): void {
  if (snapshot) {
    qc.setQueryData<InfiniteNotificationsCache>(notificationKeys.infinite(), snapshot);
  }
}

interface MutationSnapshot {
  prevList?: NotificationItem[];
  prevInfinite?: InfiniteNotificationsCache | undefined;
  prevCount?: number;
}

/** POST /notifications/{id}/read — optimistic mark-read. */
export function useMarkNotificationRead(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      await fetchJson<unknown>(`${apiBaseUrl}/notifications/${id}/read`, {
        method: 'POST',
      });
    },
    onMutate: async (id) => {
      // Cancel both the offset list and the infinite list so neither
      // refetches over our optimistic patch before the mutation settles.
      await qc.cancelQueries({ queryKey: [...notificationKeys.all, 'list'] });
      await qc.cancelQueries({ queryKey: notificationKeys.unreadCount() });

      const prevList = qc.getQueryData<NotificationItem[]>(notificationKeys.list());
      const prevCount = qc.getQueryData<number>(notificationKeys.unreadCount());

      const now = Math.floor(Date.now() / 1000);
      if (prevList) {
        qc.setQueryData(
          notificationKeys.list(),
          prevList.map((item) => (item.id === id ? { ...item, readAt: now } : item)),
        );
      }
      const prevInfinite = patchInfiniteCache(qc, (item) =>
        item.id === id ? { ...item, readAt: now } : item,
      );
      if (prevCount !== undefined && prevCount > 0) {
        qc.setQueryData(notificationKeys.unreadCount(), prevCount - 1);
      }

      const snapshot: MutationSnapshot = {};
      if (prevList !== undefined) snapshot.prevList = prevList;
      if (prevInfinite !== undefined) snapshot.prevInfinite = prevInfinite;
      if (prevCount !== undefined) snapshot.prevCount = prevCount;
      return snapshot;
    },
    onError: (_err, _id, ctx) => {
      const snap = ctx as MutationSnapshot | undefined;
      if (!snap) return;
      if (snap.prevList !== undefined) {
        qc.setQueryData(notificationKeys.list(), snap.prevList);
      }
      restoreInfiniteCache(qc, snap.prevInfinite);
      if (snap.prevCount !== undefined) {
        qc.setQueryData(notificationKeys.unreadCount(), snap.prevCount);
      }
    },
    onSettled: () => {
      // Broadcast the shared list prefix so both the legacy single-page
      // query (`notificationKeys.list()`) and the cursor-paginated
      // infinite query (`notificationKeys.infinite()`) are refetched.
      void qc.invalidateQueries({ queryKey: [...notificationKeys.all, 'list'] });
      void qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}

/** POST /notifications/{id}/archive — optimistic removal. */
export function useArchiveNotification(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      await fetchJson<unknown>(`${apiBaseUrl}/notifications/${id}/archive`, {
        method: 'POST',
      });
    },
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: [...notificationKeys.all, 'list'] });
      await qc.cancelQueries({ queryKey: notificationKeys.unreadCount() });

      const prevList = qc.getQueryData<NotificationItem[]>(notificationKeys.list());
      const prevCount = qc.getQueryData<number>(notificationKeys.unreadCount());

      // Track whether the archived row was unread anywhere, so we can
      // decrement the badge once across both caches.
      let wasUnread = false;
      if (prevList) {
        const removed = prevList.find((item) => item.id === id);
        if (removed && removed.readAt === null) wasUnread = true;
        qc.setQueryData(
          notificationKeys.list(),
          prevList.filter((item) => item.id !== id),
        );
      }
      const prevInfinite = patchInfiniteCache(qc, (item) => {
        if (item.id !== id) return item;
        if (item.readAt === null) wasUnread = true;
        return null;
      });
      if (wasUnread && prevCount !== undefined && prevCount > 0) {
        qc.setQueryData(notificationKeys.unreadCount(), prevCount - 1);
      }

      const snapshot: MutationSnapshot = {};
      if (prevList !== undefined) snapshot.prevList = prevList;
      if (prevInfinite !== undefined) snapshot.prevInfinite = prevInfinite;
      if (prevCount !== undefined) snapshot.prevCount = prevCount;
      return snapshot;
    },
    onError: (_err, _id, ctx) => {
      const snap = ctx as MutationSnapshot | undefined;
      if (!snap) return;
      if (snap.prevList !== undefined) {
        qc.setQueryData(notificationKeys.list(), snap.prevList);
      }
      restoreInfiniteCache(qc, snap.prevInfinite);
      if (snap.prevCount !== undefined) {
        qc.setQueryData(notificationKeys.unreadCount(), snap.prevCount);
      }
    },
    onSettled: () => {
      // Broadcast the shared list prefix so both the legacy single-page
      // query (`notificationKeys.list()`) and the cursor-paginated
      // infinite query (`notificationKeys.infinite()`) are refetched.
      void qc.invalidateQueries({ queryKey: [...notificationKeys.all, 'list'] });
      void qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}

/** POST /workspaces/{wsId}/notifications/read-all — marks all read. */
export function useMarkAllRead(wsId: string | null): UseMutationResult<void, ApiError, void> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (): Promise<void> => {
      if (!wsId) throw new ApiError(undefined, 'No workspace selected');
      await fetchJson<unknown>(`${apiBaseUrl}/workspaces/${wsId}/notifications/read-all`, {
        method: 'POST',
      });
    },
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: [...notificationKeys.all, 'list'] });
      await qc.cancelQueries({ queryKey: notificationKeys.unreadCount() });

      const prevList = qc.getQueryData<NotificationItem[]>(notificationKeys.list());
      const prevCount = qc.getQueryData<number>(notificationKeys.unreadCount());

      const now = Math.floor(Date.now() / 1000);
      if (prevList) {
        qc.setQueryData(
          notificationKeys.list(),
          prevList.map((item) => (item.readAt === null ? { ...item, readAt: now } : item)),
        );
      }
      const prevInfinite = patchInfiniteCache(qc, (item) =>
        item.readAt === null ? { ...item, readAt: now } : item,
      );
      qc.setQueryData(notificationKeys.unreadCount(), 0);

      const snapshot: MutationSnapshot = {};
      if (prevList !== undefined) snapshot.prevList = prevList;
      if (prevInfinite !== undefined) snapshot.prevInfinite = prevInfinite;
      if (prevCount !== undefined) snapshot.prevCount = prevCount;
      return snapshot;
    },
    onError: (_err, _vars, ctx) => {
      const snap = ctx as MutationSnapshot | undefined;
      if (!snap) return;
      if (snap.prevList !== undefined) {
        qc.setQueryData(notificationKeys.list(), snap.prevList);
      }
      restoreInfiniteCache(qc, snap.prevInfinite);
      if (snap.prevCount !== undefined) {
        qc.setQueryData(notificationKeys.unreadCount(), snap.prevCount);
      }
    },
    onSettled: () => {
      // Broadcast the shared list prefix so both the legacy single-page
      // query (`notificationKeys.list()`) and the cursor-paginated
      // infinite query (`notificationKeys.infinite()`) are refetched.
      void qc.invalidateQueries({ queryKey: [...notificationKeys.all, 'list'] });
      void qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}
