/**
 * Imports feature — typed queries and mutations.
 */
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';

export interface ImportJob {
  id: string;
  source: string;
  status: string;
  totalItems: number;
  processedItems: number;
  failedItems: number;
  errorLog?: string;
  startedAt?: number;
  completedAt?: number;
  createdAt: number;
}

/** Query key factory for the imports feature. */
export const importsKeys = {
  all: ['imports'] as const,
  list: (workspaceId: string) => [...importsKeys.all, 'list', workspaceId] as const,
  detail: (importId: string) => [...importsKeys.all, 'detail', importId] as const,
};

export function useImportsQuery(workspaceId: string): UseSuspenseQueryResult<ImportJob[]> {
  return useSuspenseQuery({
    queryKey: importsKeys.list(workspaceId),
    queryFn: async (): Promise<ImportJob[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/imports', {
            params: { path: { wsId: workspaceId }, query: { limit: 100, offset: 0 } },
          }),
        'Failed to load import jobs',
      );
      return (data.items ?? []) as ImportJob[];
    },
  });
}

export type ImportSource = 'github' | 'jira' | 'linear' | 'csv';

export interface CreateImportArgs {
  workspaceId: string;
  source: ImportSource;
  projectId?: string;
  configJson?: Record<string, unknown>;
}

export function useCreateImport(): UseMutationResult<ImportJob, ApiError, CreateImportArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      workspaceId,
      source,
      projectId,
      configJson,
    }: CreateImportArgs): Promise<ImportJob> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/imports', {
            params: { path: { wsId: workspaceId } },
            body: {
              source,
              ...(projectId != null ? { projectId } : {}),
              ...(configJson != null ? { configJson } : {}),
            },
          }),
        'Failed to create import job',
      );
      return data as ImportJob;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: importsKeys.list(vars.workspaceId) });
    },
  });
}

export interface CancelImportArgs {
  workspaceId: string;
  importId: string;
}

export function useCancelImport(): UseMutationResult<void, ApiError, CancelImportArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, importId }: CancelImportArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/imports/{importId}/cancel', {
            params: { path: { wsId: workspaceId, importId } },
          }),
        'Failed to cancel import job',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: importsKeys.list(vars.workspaceId) });
    },
  });
}
