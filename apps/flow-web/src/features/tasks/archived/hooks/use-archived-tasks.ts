/**
 * useArchivedTasksQuery — cursor-paginated archived task list for a workspace.
 *
 * Backed by `GET /workspaces/{wsId}/tasks/archived`. Each page response is
 * `{ tasks: TaskListItem[], total: number, nextCursor: string | null }`.
 * The hook is a thin Suspense + infinite-query wrapper so the Archive page
 * still renders inside `<Suspense>` + `<ErrorBoundary>` like every other
 * suspense-driven surface in flow-web while transparently flattening pages
 * into a single `tasks` array for the consumer.
 *
 * Pagination is keyset/cursor-based; see `ListArchivedTasksInput` on the
 * API side (max 200/page). We request 100/page — comfortably under the cap
 * with a fast first paint and a sane prefetch cadence as the user scrolls.
 */

import { useSuspenseInfiniteQuery } from '@tanstack/react-query';

import { toApiError } from '../../../../lib/api-error';
import { sdk } from '../../../../lib/sdk';
import type { TaskListItem } from '../../api';

/** Page size requested per call. Max allowed by the API is 200. */
const PAGE_SIZE = 100;

/** Query key factory for the archived-tasks list. */
export const archivedTasksKeys = {
  all: ['archived-tasks'] as const,
  list: (workspaceId: string) => [...archivedTasksKeys.all, 'list', workspaceId] as const,
};

/** Shape of a single page returned from the API. */
export interface ArchivedTasksPage {
  tasks: TaskListItem[];
  total: number;
  nextCursor: string | null;
}

/** Result surface the consumer interacts with. */
export interface ArchivedTasksResult {
  /** Tasks flattened across every loaded page, in API order. */
  tasks: TaskListItem[];
  /** Total archived rows in the workspace, reported by the first page. */
  total: number;
  /** True iff another page is available. */
  hasNextPage: boolean;
  /** Trigger a fetch of the next page. Safe to call when no page is pending. */
  fetchNextPage: () => Promise<unknown>;
  /** True while the next page is in flight. */
  isFetchingNextPage: boolean;
}

/**
 * Fetch the archived tasks for a workspace using cursor pagination. Errors
 * throw to the route's ErrorBoundary; an empty result is rendered by the
 * page itself.
 */
export function useArchivedTasksQuery(workspaceId: string): ArchivedTasksResult {
  const query = useSuspenseInfiniteQuery({
    queryKey: archivedTasksKeys.list(workspaceId),
    initialPageParam: '' as string,
    queryFn: async ({ pageParam }): Promise<ArchivedTasksPage> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/tasks/archived', {
        params: {
          path: { wsId: workspaceId },
          query: {
            limit: PAGE_SIZE,
            ...(pageParam ? { cursor: pageParam } : {}),
          },
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to load archived tasks');
      return {
        tasks: data.tasks ?? [],
        total: data.total ?? 0,
        nextCursor: data.nextCursor ?? null,
      };
    },
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  });

  const tasks = query.data.pages.flatMap((p) => p.tasks);
  const total = query.data.pages[0]?.total ?? 0;

  return {
    tasks,
    total,
    hasNextPage: query.hasNextPage,
    fetchNextPage: query.fetchNextPage,
    isFetchingNextPage: query.isFetchingNextPage,
  };
}
