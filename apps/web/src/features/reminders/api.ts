/**
 * Reminders feature — GET /workspaces/{wsId}/ai/reminders.
 *
 * Surfaces the workspace-wide deterministic reminder engine results
 * (2.AI-4) for the glass dock feed. Non-suspense so the dock degrades
 * gracefully when the endpoint fails.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type TaskReminder = components['schemas']['TaskReminder'];

const POLL_INTERVAL_MS = 60_000;

export const remindersKeys = {
  all: ['reminders'] as const,
  list: (workspaceId: string) => [...remindersKeys.all, 'list', workspaceId] as const,
};

/**
 * useRemindersQuery — non-suspense list of workspace-wide task
 * reminders. Polls every 60s and tolerates errors silently.
 */
export function useRemindersQuery(
  workspaceId: string | undefined,
): UseQueryResult<TaskReminder[], Error> {
  return useQuery<TaskReminder[], Error>({
    queryKey: remindersKeys.list(workspaceId ?? ''),
    enabled: typeof workspaceId === 'string' && workspaceId.length > 0,
    refetchInterval: POLL_INTERVAL_MS,
    staleTime: POLL_INTERVAL_MS,
    retry: false,
    queryFn: async (): Promise<TaskReminder[]> => {
      if (!workspaceId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/reminders', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw new Error('Failed to load reminders');
      return data.reminders ?? [];
    },
  });
}
