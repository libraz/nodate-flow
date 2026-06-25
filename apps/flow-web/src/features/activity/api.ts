/**
 * Activity feed feature — cursor-paginated infinite query over the unified
 * workspace activity stream (audit + ai + mcp), backed by the SDK.
 *
 * Mirrors the timeline / audit-log feature idioms: a typed SDK schema alias,
 * a query-key factory, and a single named hook. Unlike those offset-paged
 * lists, the activity feed is keyset-paginated via the opaque `nextCursor`
 * returned by each page, so it uses `useInfiniteQuery` instead of suspense.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type InfiniteData,
  type UseInfiniteQueryResult,
  useInfiniteQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

export type ActivityEntry = components['schemas']['Entry'];

/** Originating stream of an activity entry. */
export type ActivitySource = 'audit' | 'ai' | 'mcp';

/** Actor classification carried by an activity entry. */
export type ActivityActorKind = 'user' | 'agent' | 'system';

/** Derived severity carried by an activity entry. */
export type ActivitySeverity = 'info' | 'warn' | 'error';

/** Selectable source filter values, including the "all sources" sentinel. */
export type ActivitySourceFilter = ActivitySource | 'all';

export const ACTIVITY_SOURCES: readonly ActivitySource[] = ['audit', 'ai', 'mcp'];

export interface ActivityFilters {
  /** Originating-stream filter. `'all'` (or undefined) omits the filter. */
  source?: ActivitySourceFilter;
  /** Page size hint forwarded to the backend. */
  limit?: number;
}

/** One page of the activity feed as returned by the SDK. */
export interface ActivityPage {
  activity: ActivityEntry[];
  total: number;
  nextCursor: string | null;
}

/** Query key factory for the activity feed feature. */
export const activityKeys = {
  all: ['activity'] as const,
  list: (workspaceId: string, filters: ActivityFilters) =>
    [...activityKeys.all, 'list', workspaceId, filters] as const,
};

const DEFAULT_LIMIT = 50;

function normalize(data: {
  activity?: ActivityEntry[] | null;
  total?: number;
  nextCursor?: string | null;
}): ActivityPage {
  return {
    activity: data.activity ?? [],
    total: data.total ?? 0,
    nextCursor: data.nextCursor ?? null,
  };
}

/**
 * useActivityFeedQuery — cursor-paginated infinite list of unified activity
 * for the given workspace. Backed by GET /workspaces/{wsId}/activity.
 *
 * The `source` filter is sent only for a concrete stream; `'all'` (the
 * default) omits the query param so the backend returns the full union.
 * `nextCursor` from each page seeds the next request; a `null` cursor marks
 * the end of the feed and disables further fetching.
 *
 * @param workspaceId - Workspace public id (UUID v7).
 * @param filters - Source filter and page-size hint.
 * @returns A TanStack infinite-query result paginated by opaque cursor.
 */
export function useActivityFeedQuery(
  workspaceId: string,
  filters: ActivityFilters = {},
): UseInfiniteQueryResult<InfiniteData<ActivityPage>, ApiError> {
  const limit = filters.limit ?? DEFAULT_LIMIT;
  const source = filters.source && filters.source !== 'all' ? filters.source : undefined;

  return useInfiniteQuery({
    queryKey: activityKeys.list(workspaceId, filters),
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }): Promise<ActivityPage> => {
      const query: { source?: ActivitySource; cursor?: string; limit?: number } = { limit };
      if (source !== undefined) query.source = source;
      if (pageParam !== undefined) query.cursor = pageParam;

      const { data, error } = await sdk.GET('/workspaces/{wsId}/activity', {
        params: { path: { wsId: workspaceId }, query },
      });
      if (error || !data) throw toApiError(error, 'Failed to load activity feed');
      return normalize(data);
    },
    getNextPageParam: (lastPage): string | undefined =>
      lastPage.nextCursor !== null && lastPage.nextCursor.length > 0
        ? lastPage.nextCursor
        : undefined,
  });
}
