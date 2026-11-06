import type { components } from '@nodate-flow/sdk';
import { useSuspenseQuery } from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';
import { tasksKeys } from './api';

export type TaskSource = components['schemas']['TaskSource'];

export function useTaskSourcesQuery(taskId: string): TaskSource[] {
  return useSuspenseQuery({
    queryKey: tasksKeys.sources(taskId),
    queryFn: async (): Promise<TaskSource[]> => {
      const data = await apiRequest(
        (client) => client.GET('/tasks/{id}/sources', { params: { path: { id: taskId } } }),
        'Failed to load task sources',
      );
      return data.sources ?? [];
    },
  }).data;
}
