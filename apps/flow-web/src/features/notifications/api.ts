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
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
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

/** Query key factory for the notifications feature. */
export const notificationKeys = {
  all: ['notifications'] as const,
  list: () => [...notificationKeys.all, 'list'] as const,
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

/**
 * GET /me/notifications — suspense query for the notification list.
 *
 * `useSuspenseQuery` always rethrows on error by design (it has no
 * `throwOnError` opt-out), so containment for the notifications panel
 * lives at the call site: NotificationBell wraps the `<Suspense>`
 * hosting `<NotificationDropdown>` in a local `<ErrorBoundary
 * fallback={null}>`, which catches anything this query throws before
 * it can cascade to the route ErrorBoundary.
 */
export function useNotificationsQuery(): UseSuspenseQueryResult<NotificationItem[]> {
  return useSuspenseQuery({
    queryKey: notificationKeys.list(),
    queryFn: async (): Promise<NotificationItem[]> => {
      const data = await fetchJson<{ items?: NotificationItem[] }>(
        `${apiBaseUrl}/me/notifications?limit=50`,
      );
      return data.items ?? [];
    },
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
      await qc.cancelQueries({ queryKey: notificationKeys.list() });
      await qc.cancelQueries({ queryKey: notificationKeys.unreadCount() });

      const prevList = qc.getQueryData<NotificationItem[]>(notificationKeys.list());
      const prevCount = qc.getQueryData<number>(notificationKeys.unreadCount());

      if (prevList) {
        const now = Math.floor(Date.now() / 1000);
        qc.setQueryData(
          notificationKeys.list(),
          prevList.map((item) => (item.id === id ? { ...item, readAt: now } : item)),
        );
      }
      if (prevCount !== undefined && prevCount > 0) {
        qc.setQueryData(notificationKeys.unreadCount(), prevCount - 1);
      }

      return { prevList, prevCount };
    },
    onError: (_err, _id, ctx) => {
      const snap = ctx as { prevList?: NotificationItem[]; prevCount?: number } | undefined;
      if (!snap) return;
      if (snap.prevList !== undefined) {
        qc.setQueryData(notificationKeys.list(), snap.prevList);
      }
      if (snap.prevCount !== undefined) {
        qc.setQueryData(notificationKeys.unreadCount(), snap.prevCount);
      }
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: notificationKeys.list() });
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
      await qc.cancelQueries({ queryKey: notificationKeys.list() });
      await qc.cancelQueries({ queryKey: notificationKeys.unreadCount() });

      const prevList = qc.getQueryData<NotificationItem[]>(notificationKeys.list());
      const prevCount = qc.getQueryData<number>(notificationKeys.unreadCount());

      if (prevList) {
        const removed = prevList.find((item) => item.id === id);
        qc.setQueryData(
          notificationKeys.list(),
          prevList.filter((item) => item.id !== id),
        );
        // Decrement unread count if the archived item was unread
        if (removed && removed.readAt === null && prevCount !== undefined && prevCount > 0) {
          qc.setQueryData(notificationKeys.unreadCount(), prevCount - 1);
        }
      }

      return { prevList, prevCount };
    },
    onError: (_err, _id, ctx) => {
      const snap = ctx as { prevList?: NotificationItem[]; prevCount?: number } | undefined;
      if (!snap) return;
      if (snap.prevList !== undefined) {
        qc.setQueryData(notificationKeys.list(), snap.prevList);
      }
      if (snap.prevCount !== undefined) {
        qc.setQueryData(notificationKeys.unreadCount(), snap.prevCount);
      }
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: notificationKeys.list() });
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
      await qc.cancelQueries({ queryKey: notificationKeys.list() });
      await qc.cancelQueries({ queryKey: notificationKeys.unreadCount() });

      const prevList = qc.getQueryData<NotificationItem[]>(notificationKeys.list());
      const prevCount = qc.getQueryData<number>(notificationKeys.unreadCount());

      if (prevList) {
        const now = Math.floor(Date.now() / 1000);
        qc.setQueryData(
          notificationKeys.list(),
          prevList.map((item) => (item.readAt === null ? { ...item, readAt: now } : item)),
        );
      }
      qc.setQueryData(notificationKeys.unreadCount(), 0);

      return { prevList, prevCount };
    },
    onError: (_err, _vars, ctx) => {
      const snap = ctx as { prevList?: NotificationItem[]; prevCount?: number } | undefined;
      if (!snap) return;
      if (snap.prevList !== undefined) {
        qc.setQueryData(notificationKeys.list(), snap.prevList);
      }
      if (snap.prevCount !== undefined) {
        qc.setQueryData(notificationKeys.unreadCount(), snap.prevCount);
      }
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: notificationKeys.list() });
      void qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}
