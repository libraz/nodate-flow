/**
 * useLinkedEventsQuery — list of calendar events linked to a task.
 *
 * Backed by `GET /tasks/{id}/linked-events`. The endpoint returns
 * `{ links: TaskEventLink[], total: number }` where each link carries
 * the event title, calendar id, start/end timestamps, and the relation
 * kind. The response is rendered by the Linked Events sub-section of
 * the task detail page; the hook is suspense-driven so the section sits
 * inside its own `<Suspense>` + `<ErrorBoundary>` boundary in the page.
 */

import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';

import { toApiError } from '../../../../lib/api-error';
import { sdk } from '../../../../lib/sdk';
import type { TaskEventLink } from '../types';

/** Query key factory for the linked-events list per task. */
export const linkedEventsKeys = {
  all: ['linked-events'] as const,
  list: (taskId: string) => [...linkedEventsKeys.all, 'list', taskId] as const,
};

export interface LinkedEventsResult {
  links: TaskEventLink[];
  total: number;
}

/**
 * Fetch linked events for a task. Throws to the nearest ErrorBoundary on
 * failure so the section's local error UI can render `error.fetchFailed`.
 */
export function useLinkedEventsQuery(taskId: string): UseSuspenseQueryResult<LinkedEventsResult> {
  return useSuspenseQuery({
    queryKey: linkedEventsKeys.list(taskId),
    queryFn: async (): Promise<LinkedEventsResult> => {
      const { data, error } = await sdk.GET('/tasks/{id}/linked-events', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load linked events');
      return { links: data.links ?? [], total: data.total ?? 0 };
    },
  });
}
