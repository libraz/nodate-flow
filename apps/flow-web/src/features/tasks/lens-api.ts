/**
 * Lens (saved views) feature — typed queries and mutations.
 *
 * The SDK's SavedLens type is mapped to the narrower LensDto at the
 * boundary via `toLensDto`. All hooks follow the same patterns as `./api.ts`.
 *
 * Cache invalidation policy
 * -------------------------
 *   - Create  → invalidate the parent list key for the (workspaceId,
 *               projectId) scope. The two args together uniquely
 *               identify the saved-view list the new lens appears in.
 *   - Delete  → same (the list shrinks).
 *   - Update  → not exposed yet; when added, it must invalidate the
 *               same list scope plus a detail key once one exists.
 *   - Publish / unpublish  → owned by the sharing feature, which does not
 *               know the list scope. Its caller invalidates this list so a
 *               row's `isPublic` does not go stale.
 *
 * Lenses do not feed any other cache today, so there is no cross-key
 * fan-out. Keep the scope tight to avoid disturbing tasks / projects
 * caches that share neighbouring routes.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

/** SDK type for the lens response object. */
type SavedLens = components['schemas']['SavedLens'];

/** DTO returned by the lenses API endpoints. */
export interface LensDto {
  id: string;
  creatorId: string;
  creatorDisplayName: string;
  name: string;
  description?: string;
  filter: Record<string, Record<string, unknown>>;
  sort: Array<{ field: string; dir: string }>;
  groupBy: string | null;
  isDefault: boolean;
  /**
   * Whether the lens is exposed on an unauthenticated share URL. The
   * plaintext share token is not part of this record — the API returns it
   * only in the publish response — so this is the only durable signal that
   * a saved view is public.
   */
  isPublic: boolean;
  sortWeight: number;
  updatedAt?: number;
  createdAt: number;
}

/**
 * Maps the SDK's SavedLens response to the narrower LensDto used by the
 * UI. The SDK types `filter` and `sort` as `unknown` because the OpenAPI
 * spec uses a free-form object; we narrow them here at the boundary.
 */
function toLensDto(lens: SavedLens): LensDto {
  const dto: LensDto = {
    id: lens.id,
    creatorId: lens.creatorId,
    creatorDisplayName: lens.creatorDisplayName,
    name: lens.name,
    filter: (lens.filter ?? {}) as Record<string, Record<string, unknown>>,
    sort: (lens.sort ?? []) as Array<{ field: string; dir: string }>,
    groupBy: lens.groupBy,
    isDefault: lens.isDefault,
    isPublic: lens.isPublic,
    sortWeight: lens.sortWeight,
    createdAt: lens.createdAt,
  };
  if (lens.description != null) {
    dto.description = lens.description;
  }
  if (lens.updatedAt != null) {
    dto.updatedAt = lens.updatedAt;
  }
  return dto;
}

import { ApiError } from '../../lib/api-error';

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
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/lenses', {
            params: { path: { wsId: workspaceId }, query },
          }),
        'Failed to load saved views',
      );
      return (data.lenses ?? []).map(toLensDto);
    },
  });
}

export interface CreateLensArgs {
  workspaceId: string;
  name: string;
  description?: string;
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
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/lenses', {
            params: { path: { wsId: args.workspaceId } },
            body: {
              name: args.name,
              ...(args.description ? { description: args.description } : {}),
              ...(args.projectId ? { projectId: args.projectId } : {}),
              filter: args.filter,
              sort: args.sort ?? [],
              ...(args.groupBy ? { groupBy: args.groupBy } : {}),
              isDefault: false,
            },
          }),
        'Failed to save view',
      );
      return toLensDto(data);
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
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/lenses/{lensId}', {
            params: { path: { wsId: workspaceId, lensId } },
          }),
        'Failed to delete view',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: lensesKeys.list(vars.workspaceId, vars.projectId) });
    },
  });
}
