/**
 * Favorites feature — typed queries and mutations.
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

// TODO: Replace with SDK types after `bun run generate:sdk`
export interface Favorite {
  id: string;
  targetType: string;
  targetId: string;
  folderName?: string;
  createdAt: number;
}

/** Query key factory for the favorites feature. */
export const favoritesKeys = {
  all: ['favorites'] as const,
  list: (workspaceId: string) => [...favoritesKeys.all, 'list', workspaceId] as const,
};

export function useFavoritesQuery(workspaceId: string): UseSuspenseQueryResult<Favorite[]> {
  return useSuspenseQuery({
    queryKey: favoritesKeys.list(workspaceId),
    queryFn: async (): Promise<Favorite[]> => {
      const { data, error } = await sdk.GET('/me/favorites', {
        params: { query: { workspaceId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load favorites');
      return (data as { favorites?: Favorite[] }).favorites ?? [];
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
      const { data, error } = await sdk.POST('/me/favorites', {
        body: { workspaceId, targetType, targetId },
      });
      if (error || !data) throw toApiError(error, 'Failed to add favorite');
      return data as Favorite;
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
    mutationFn: async ({ id }: RemoveFavoriteArgs): Promise<void> => {
      const { error } = await sdk.DELETE('/me/favorites/{id}', {
        params: { path: { id } },
      });
      if (error) throw toApiError(error, 'Failed to remove favorite');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: favoritesKeys.list(vars.workspaceId) });
    },
  });
}
