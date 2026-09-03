/**
 * Task replay feature — GET /tasks/{id}/replay.
 *
 * Powers the 3.WEB-3 timeline replay panel: recomputes derived_state
 * from the task.transition.* event log and reports whether replay
 * agrees with the stored tasks.derived_state.
 */

import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

export interface ReplayResult {
  derivedState: string;
  stored: string;
  equivalent: boolean;
}

export const replayKeys = {
  all: ['replay'] as const,
  task: (taskId: string) => [...replayKeys.all, 'task', taskId] as const,
};

/**
 * useTaskReplayQuery — non-suspense query that re-derives a task's
 * state from its event log. Disabled until a taskId is known.
 */
export function useTaskReplayQuery(
  taskId: string | undefined,
): UseQueryResult<ReplayResult, Error> {
  return useQuery<ReplayResult, Error>({
    queryKey: replayKeys.task(taskId ?? ''),
    enabled: typeof taskId === 'string' && taskId.length > 0,
    staleTime: 30_000,
    retry: false,
    queryFn: async (): Promise<ReplayResult> => {
      if (!taskId) throw new Error('taskId required');
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/replay', {
            params: { path: { id: taskId } },
          }),
        'Failed to replay task',
      );
      return {
        derivedState: data.derivedState,
        stored: data.stored,
        equivalent: data.equivalent,
      };
    },
  });
}
