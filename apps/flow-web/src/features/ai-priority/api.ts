/**
 * AI priority suggestions feature — typed query against the
 * `GET /workspaces/{wsId}/ai/priority-suggestions` endpoint.
 *
 * Suspense-ready (`useSuspenseQuery`) so the page route renders inside the
 * shared `<Suspense>` + `<ErrorBoundary>` wrappers. There is no dedicated
 * apply / dismiss endpoint — application is performed via the existing
 * `useUpdateTask` (`PATCH /tasks/{id}` with `{ priority }`), and dismissals
 * are stored client-side (see `./dismiss-store.ts`).
 */

import type { components } from '@nodate-flow/sdk';
import { type UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';

/** Single priority adjustment proposal. Mirrors the SDK schema. */
export type PrioritySuggestion = components['schemas']['TaskPrioritySuggestion'];

/** Response payload for the priority suggestions list endpoint. */
export interface PrioritySuggestionsResult {
  /** Number of open tasks evaluated server-side (denominator for the KPI). */
  total: number;
  /** Suggestions whose current priority differs from the recommended one. */
  suggestions: PrioritySuggestion[];
}

/** Query key factory for AI priority suggestions. */
export const aiPriorityKeys = {
  all: ['ai-priority'] as const,
  list: (workspaceId: string) => [...aiPriorityKeys.all, 'list', workspaceId] as const,
};

/**
 * useAiPrioritySuggestionsQuery — suspense list of priority suggestions
 * for the given workspace. Errors propagate to the route ErrorBoundary,
 * matching the rest of the workspace surface.
 */
export function useAiPrioritySuggestionsQuery(
  workspaceId: string,
): UseSuspenseQueryResult<PrioritySuggestionsResult, ApiError> {
  return useSuspenseQuery<PrioritySuggestionsResult, ApiError>({
    queryKey: aiPriorityKeys.list(workspaceId),
    queryFn: async (): Promise<PrioritySuggestionsResult> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/priority-suggestions', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load priority suggestions',
      );
      return {
        total: data.total,
        suggestions: data.suggestions ?? [],
      };
    },
  });
}
