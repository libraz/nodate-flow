/**
 * Notifications feature — query key factory, types, and hooks for
 * notification list, unread count, mark-read, archive, and mark-all-read.
 *
 * All calls go through the typed `@nodate-flow/sdk` so request and
 * response shapes stay aligned with the OpenAPI contract.
 */

import type { components } from '@nodate-flow/sdk';
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
import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';

/**
 * Notification severity — narrowed from the SDK's open `string` field on
 * `NotificationDTO` so consumers can switch on the four documented
 * values without an `as` cast.
 */
export type NotificationSeverity = 'low' | 'normal' | 'high' | 'urgent';

/**
 * Notification item returned by the API. Widens / narrows a few fields
 * vs the SDK's `NotificationDTO`:
 *  - `severity` narrowed to the documented union
 *  - `actorId`, `actorDisplayName`, `body`, `resourceId` flattened from
 *    optional to nullable, matching the previous wire contract the rest
 *    of the panel was written against.
 *  - `total` retained on each item (the legacy single-page list flowed
 *    it through item rows; new code should prefer the page envelope).
 */
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
  severity: NotificationSeverity;
  channel: string;
  readAt: number | null;
  deliveredAt: number | null;
  createdAt: number;
  total: number;
}

/** Convert an SDK `NotificationDTO` row into the local `NotificationItem`. */
function dtoToItem(dto: components['schemas']['NotificationDTO'], total: number): NotificationItem {
  return {
    id: dto.id,
    workspaceId: dto.workspaceId,
    actorId: dto.actorId ?? null,
    actorDisplayName: dto.actorDisplayName ?? null,
    eventType: dto.eventType,
    resourceType: dto.resourceType,
    resourceId: dto.resourceId ?? null,
    title: dto.title,
    body: dto.body ?? null,
    severity: dto.severity as NotificationSeverity,
    channel: dto.channel,
    readAt: dto.readAt,
    deliveredAt: dto.deliveredAt,
    createdAt: dto.createdAt,
    total,
  };
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

export { ApiError as NotificationApiError };

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
      const data = await apiRequest(
        (client) =>
          client.GET('/me/notifications', {
            params: { query: { limit: 50 } },
          }),
        'Failed to load notifications',
      );
      const total = data.total;
      return (data.notifications ?? []).map((dto) => dtoToItem(dto, total));
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
      const data = await apiRequest(
        (client) =>
          client.GET('/me/notifications', {
            params: {
              query: {
                limit: NOTIFICATIONS_PAGE_SIZE,
                ...(pageParam ? { cursor: pageParam } : {}),
              },
            },
          }),
        'Failed to load notifications',
      );
      const total = data.total;
      return {
        notifications: (data.notifications ?? []).map((dto) => dtoToItem(dto, total)),
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
      // The requester threads `response.status` into the thrown error,
      // which is what the global QueryCache 401 handler in the shared
      // SDK QueryClient reads to detect a terminal auth failure on this
      // poll and stop it / bounce the user to /login.
      const data = await apiRequest(
        (client) => client.GET('/me/notifications/unread-count'),
        'Failed to load unread count',
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
      await apiRequest(
        (client) =>
          client.POST('/notifications/{notifId}/read', {
            params: { path: { notifId: id } },
          }),
        'Failed to mark notification read',
      );
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

/**
 * The archive endpoint only matches a notification the caller owns that is
 * still unarchived, so it answers this code whenever it archived nothing:
 * the row is already archived, belongs to someone else, or never existed.
 * In every one of those cases the notification is not in the caller's list,
 * which is precisely the state the archive action asks for — so we treat it
 * as success rather than rolling the optimistic removal back and reporting a
 * failure for something the user already got.
 *
 * Narrow on purpose: only this code, only on this endpoint. Any other
 * refusal is a real failure and still rolls back and surfaces.
 */
const ARCHIVE_ALREADY_GONE_CODE = 'WS.NOTIFICATION.NOT_FOUND';

/** POST /notifications/{id}/archive — optimistic removal. */
export function useArchiveNotification(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      try {
        await apiRequest(
          (client) =>
            client.POST('/notifications/{notifId}/archive', {
              params: { path: { notifId: id } },
            }),
          'Failed to archive notification',
        );
      } catch (err) {
        if (err instanceof ApiError && err.code === ARCHIVE_ALREADY_GONE_CODE) return;
        throw err;
      }
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
      await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/notifications/read-all', {
            params: { path: { wsId } },
          }),
        'Failed to mark all notifications read',
      );
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
