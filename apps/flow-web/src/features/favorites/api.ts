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

export type Favorite = components['schemas']['Favorite'];

/** Query key factory for the favorites feature. */
export const favoritesKeys = {
  all: ['favorites'] as const,
  list: (workspaceId: string) => [...favoritesKeys.all, 'list', workspaceId] as const,
};

export function useFavoritesQuery(workspaceId: string): UseSuspenseQueryResult<Favorite[]> {
  return useSuspenseQuery({
    queryKey: favoritesKeys.list(workspaceId),
    queryFn: async (): Promise<Favorite[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/me/favorites', {
            params: { query: { workspaceId } },
          }),
        'Failed to load favorites',
      );
      return data.favorites ?? [];
    },
  });
}

export type FavoriteTargetType = 'task' | 'project' | 'page' | 'lens' | 'timebox';

export interface AddFavoriteArgs {
  workspaceId: string;
  targetType: FavoriteTargetType;
  targetId: string;
}

export function useAddFavorite(): UseMutationResult<Favorite, ApiError, AddFavoriteArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      workspaceId,
      targetType,
      targetId,
    }: AddFavoriteArgs): Promise<Favorite> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/me/favorites', {
            body: { workspaceId, targetType, targetId },
          }),
        'Failed to add favorite',
      );
      return data;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: favoritesKeys.list(vars.workspaceId) });
    },
  });
}

export interface RemoveFavoriteArgs {
  id: string;
  workspaceId: string;
}

export function useRemoveFavorite(): UseMutationResult<void, ApiError, RemoveFavoriteArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, workspaceId }: RemoveFavoriteArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/me/favorites/{id}', {
            params: { path: { id }, query: { workspaceId } },
          }),
        'Failed to remove favorite',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: favoritesKeys.list(vars.workspaceId) });
    },
  });
}
