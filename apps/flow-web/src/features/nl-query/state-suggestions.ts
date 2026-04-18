/**
 * State suggestions feature — GET /workspaces/{wsId}/ai/state-suggestions.
 *
 * Surfaces the workspace-wide deterministic state inference proposals
 * (2.AI-1) for the glass dock feed. Non-suspense so the dock degrades
 * gracefully when the endpoint fails.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';
import { useStreamHealthy } from '../realtime/stream-health';

export type StateSuggestion = components['schemas']['StateSuggestion'];

const POLL_INTERVAL_MS = 60_000;

export const stateSuggestionsKeys = {
  all: ['state-suggestions'] as const,
  list: (workspaceId: string) => [...stateSuggestionsKeys.all, 'list', workspaceId] as const,
};

/**
 * useStateSuggestionsQuery — non-suspense list of workspace-wide state
 * inference proposals. Polls every 60s and tolerates errors silently.
 */
export function useStateSuggestionsQuery(
  workspaceId: string | undefined,
): UseQueryResult<StateSuggestion[], Error> {
  const streamHealthy = useStreamHealthy();
  return useQuery<StateSuggestion[], Error>({
    queryKey: stateSuggestionsKeys.list(workspaceId ?? ''),
    enabled: typeof workspaceId === 'string' && workspaceId.length > 0,
    refetchInterval: streamHealthy ? false : POLL_INTERVAL_MS,
    staleTime: POLL_INTERVAL_MS,
    retry: false,
    queryFn: async (): Promise<StateSuggestion[]> => {
      if (!workspaceId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/state-suggestions', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw new Error('Failed to load state suggestions');
      return data.suggestions ?? [];
    },
  });
}
