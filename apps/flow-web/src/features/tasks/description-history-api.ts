import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';
import { tasksKeys } from './api';

export type DescriptionVersion = components['schemas']['DescriptionVersion'];
export type DescriptionVersionFull = components['schemas']['DescriptionVersionFull'];

/** Query key factory for description history, nested under tasks. */
export const descriptionHistoryKeys = {
  all: (taskId: string) => [...tasksKeys.all, 'detail', taskId, 'description-history'] as const,
  version: (taskId: string, versionId: string) =>
    [...descriptionHistoryKeys.all(taskId), versionId] as const,
};

export function useDescriptionHistoryQuery(
  taskId: string,
): UseSuspenseQueryResult<DescriptionVersion[]> {
  return useSuspenseQuery({
    queryKey: descriptionHistoryKeys.all(taskId),
    queryFn: async (): Promise<DescriptionVersion[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/description-history', {
            params: { path: { id: taskId } },
          }),
        'Failed to load description history',
      );
      return data.versions ?? [];
    },
  });
}

export function useDescriptionVersionQuery(
  taskId: string,
  versionId: string,
): UseSuspenseQueryResult<DescriptionVersionFull> {
  return useSuspenseQuery({
    queryKey: descriptionHistoryKeys.version(taskId, versionId),
    queryFn: async (): Promise<DescriptionVersionFull> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/description-history/{versionId}', {
            params: { path: { id: taskId, versionId } },
          }),
        'Failed to load description version',
      );
      return data;
    },
  });
}

export interface RestoreVersionArgs {
  taskId: string;
  versionId: string;
}

export function useRestoreDescriptionVersion(): UseMutationResult<
  void,
  ApiError,
  RestoreVersionArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, versionId }: RestoreVersionArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/tasks/{id}/description-history/{versionId}/restore', {
            params: { path: { id: taskId, versionId } },
          }),
        'Failed to restore version',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: descriptionHistoryKeys.all(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
    },
  });
}
