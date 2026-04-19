/**
 * Lens (saved views) feature — typed queries and mutations.
 *
 * The SDK types may not be generated yet, so the DTO is defined inline.
 * All hooks follow the same patterns as `./api.ts`.
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

/** DTO returned by the lenses API endpoints. */
export interface LensDto {
  id: string;
  creatorId: string;
  creatorDisplayName: string;
  name: string;
  filter: Record<string, Record<string, unknown>>;
  sort: Array<{ field: string; dir: string }>;
  groupBy: string | null;
  isDefault: boolean;
  sortWeight: number;
  updatedAt?: number;
  createdAt: number;
}

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as LensApiError };

/** Query key factory for the lenses feature. */
export const lensesKeys = {
  all: ['lenses'] as const,
  list: (workspaceId: string, projectId?: string) =>
    [...lensesKeys.all, workspaceId, projectId ?? ''] as const,
};

/** Fetches saved views for a workspace, optionally filtered by project. */
export function useLensesQuery(
  workspaceId: string,
  projectId?: string,
): UseSuspenseQueryResult<LensDto[]> {
  return useSuspenseQuery({
    queryKey: lensesKeys.list(workspaceId, projectId),
    queryFn: async (): Promise<LensDto[]> => {
      const query: Record<string, string> = {};
      if (projectId) query.projectId = projectId;
      const { data, error } = await sdk.GET('/workspaces/{wsId}/lenses', {
        params: { path: { wsId: workspaceId }, query },
      });
      if (error || !data) throw toApiError(error, 'Failed to load saved views');
      // The response shape is { lenses: LensDto[] }
      const lenses = (data as { lenses?: LensDto[] }).lenses ?? [];
      return lenses;
    },
  });
}

export interface CreateLensArgs {
  workspaceId: string;
  name: string;
  projectId?: string;
  filter: Record<string, Record<string, unknown>>;
  sort?: Array<{ field: string; dir: string }>;
  groupBy?: string | null;
}

/** Creates a new saved view. */
export function useCreateLens(): UseMutationResult<LensDto, ApiError, CreateLensArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: CreateLensArgs): Promise<LensDto> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/lenses', {
        params: { path: { wsId: args.workspaceId } },
        body: {
          name: args.name,
          ...(args.projectId ? { projectId: args.projectId } : {}),
          filter: args.filter,
          sort: args.sort ?? [],
          ...(args.groupBy ? { groupBy: args.groupBy } : {}),
          isDefault: false,
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to save view');
      return data as unknown as LensDto;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: lensesKeys.list(vars.workspaceId, vars.projectId) });
    },
  });
}

export interface DeleteLensArgs {
  workspaceId: string;
  lensId: string;
  projectId?: string;
}

/** Deletes a saved view. */
export function useDeleteLens(): UseMutationResult<void, ApiError, DeleteLensArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, lensId }: DeleteLensArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/lenses/{lensId}', {
        params: { path: { wsId: workspaceId, lensId } },
      });
      if (error) throw toApiError(error, 'Failed to delete view');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: lensesKeys.list(vars.workspaceId, vars.projectId) });
    },
  });
}
