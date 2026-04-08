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

import { sdk } from '../../lib/sdk';

export type Me = components['schemas']['MeBody'];
export type PatchMeInput = components['schemas']['PatchMeInputBody'];

/** Query key factory for the settings feature. */
export const settingsKeys = {
  me: ['me'] as const,
};

/** Lightweight error thrown when the SDK returns an error envelope. */
export class SettingsApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'SettingsApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): SettingsApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new SettingsApiError(code, message);
  }
  return new SettingsApiError(undefined, fallback);
}

/** GET /me — current authenticated user profile. */
export function useMeQuery(): UseSuspenseQueryResult<Me> {
  return useSuspenseQuery({
    queryKey: settingsKeys.me,
    queryFn: async (): Promise<Me> => {
      const { data, error } = await sdk.GET('/me');
      if (error || !data) throw toError(error, 'Failed to load profile');
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
export function useUpdateMe(): UseMutationResult<
  Me,
  SettingsApiError,
  PatchMeInput,
  UpdateMeContext
> {
  const qc = useQueryClient();
  return useMutation<Me, SettingsApiError, PatchMeInput, UpdateMeContext>({
    mutationFn: async (input: PatchMeInput): Promise<Me> => {
      const { data, error } = await sdk.PATCH('/me', { body: input });
      if (error || !data) throw toError(error, 'Failed to update profile');
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
  });
}
