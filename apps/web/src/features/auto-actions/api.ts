/**
 * Auto actions feature — GET /workspaces/{wsId}/ai/auto-actions.
 *
 * Surfaces the workspace-wide deterministic auto-action engine
 * results (2.AI-3) for the glass dock feed. Non-suspense so the dock
 * degrades gracefully when the endpoint fails.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';
import { useStreamHealthy } from '../realtime/stream-health';

export type TaskAutoAction = components['schemas']['TaskAutoAction'];

const POLL_INTERVAL_MS = 60_000;

export const autoActionsKeys = {
  all: ['auto-actions'] as const,
  list: (workspaceId: string) => [...autoActionsKeys.all, 'list', workspaceId] as const,
};

/**
 * useAutoActionsQuery — non-suspense list of workspace-wide task
 * auto-actions. Refreshes via the realtime SSE stream (ADR 0005);
 * falls back to 60s polling when the stream is unhealthy.
 */
export function useAutoActionsQuery(
  workspaceId: string | undefined,
): UseQueryResult<TaskAutoAction[], Error> {
  const streamHealthy = useStreamHealthy();
  return useQuery<TaskAutoAction[], Error>({
    queryKey: autoActionsKeys.list(workspaceId ?? ''),
    enabled: typeof workspaceId === 'string' && workspaceId.length > 0,
    refetchInterval: streamHealthy ? false : POLL_INTERVAL_MS,
    staleTime: POLL_INTERVAL_MS,
    retry: false,
    queryFn: async (): Promise<TaskAutoAction[]> => {
      if (!workspaceId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/auto-actions', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw new Error('Failed to load auto actions');
      return data.actions ?? [];
    },
  });
}
