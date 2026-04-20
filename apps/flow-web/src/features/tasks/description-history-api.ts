/**
 * Description version history — queries and mutations.
 */
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';
import { tasksKeys } from './api';

// TODO: Replace with SDK types after `bun run generate:sdk`
export interface DescriptionVersion {
  id: string;
  versionNumber: number;
  authorId?: string;
  authorDisplayName?: string;
  bodyLength: number;
  createdAt: number;
}

export interface DescriptionVersionFull extends DescriptionVersion {
  body: string;
}

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
      const { data, error } = await sdk.GET('/tasks/{id}/description-history', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load description history');
      return (data as { versions?: DescriptionVersion[] }).versions ?? [];
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
      const { data, error } = await sdk.GET('/tasks/{id}/description-history/{versionId}', {
        params: { path: { id: taskId, versionId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load description version');
      return data as DescriptionVersionFull;
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
      const { error } = await sdk.POST('/tasks/{id}/description-history/{versionId}/restore', {
        params: { path: { id: taskId, versionId } },
      });
      if (error) throw toApiError(error, 'Failed to restore version');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: descriptionHistoryKeys.all(vars.taskId) });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(vars.taskId) });
    },
  });
}
