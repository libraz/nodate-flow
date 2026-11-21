/**
 * Settings feature — typed Suspense queries and mutations for the
 * authenticated user's own profile (`GET /me` / `PATCH /me`).
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { authApiRequest } from '../../lib/api';

export type Me = components['schemas']['MeBody'];
export type PatchMeInput = components['schemas']['PatchMeInputBody'];

/** Query key factory for the settings feature. */
export const settingsKeys = {
  me: ['me'] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as SettingsApiError };

/** GET /me — current authenticated user profile. */
export function useMeQuery(): UseSuspenseQueryResult<Me> {
  return useSuspenseQuery({
    queryKey: settingsKeys.me,
    queryFn: async (): Promise<Me> => {
      const data = await authApiRequest((client) => client.GET('/me'), 'Failed to load profile');
      return data;
    },
  });
}

interface UpdateMeContext {
  previous: Me | undefined;
}

/**
 * PATCH /me — update profile fields with an optimistic cache update.
 * Rolls back to the previous snapshot on failure.
 */
export function useUpdateMe(): UseMutationResult<Me, ApiError, PatchMeInput, UpdateMeContext> {
  const qc = useQueryClient();
  return useMutation<Me, ApiError, PatchMeInput, UpdateMeContext>({
    throwOnError: false,
    mutationFn: async (input: PatchMeInput): Promise<Me> => {
      const data = await authApiRequest(
        (client) => client.PATCH('/me', { body: input }),
        'Failed to update profile',
      );
      return data;
    },
    onMutate: async (input: PatchMeInput): Promise<UpdateMeContext> => {
      await qc.cancelQueries({ queryKey: settingsKeys.me });
      const previous = qc.getQueryData<Me>(settingsKeys.me);
      if (previous) {
        qc.setQueryData<Me>(settingsKeys.me, { ...previous, ...input });
      }
      return { previous };
    },
    onError: (_err, _input, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData<Me>(settingsKeys.me, ctx.previous);
      }
    },
    onSuccess: (data) => {
      qc.setQueryData<Me>(settingsKeys.me, data);
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.me });
    },
  });
}
