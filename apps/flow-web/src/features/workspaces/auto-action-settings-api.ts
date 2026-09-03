/**
 * Auto-action settings — typed queries and mutations backed by the SDK.
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';

/** Shape returned by GET and PATCH auto-action-settings. */
export interface AutoActionSettings {
  enabled: boolean;
  intervalMinutes: number;
  threshold: number;
}

/** Partial input accepted by PATCH auto-action-settings. */
export interface PatchAutoActionSettingsInput {
  enabled?: boolean;
  intervalMinutes?: number;
  threshold?: number;
}

/** Query key factory for auto-action settings. */
export const autoActionSettingsKeys = {
  all: ['workspaces'] as const,
  settings: (wsId: string) => ['workspaces', wsId, 'auto-action-settings'] as const,
};

/** Suspense query for the current auto-action settings. */
export function useAutoActionSettingsQuery(
  workspaceId: string,
): UseSuspenseQueryResult<AutoActionSettings> {
  return useSuspenseQuery({
    queryKey: autoActionSettingsKeys.settings(workspaceId),
    queryFn: async (): Promise<AutoActionSettings> => {
      return apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/auto-action-settings', {
            params: { path: { wsId: workspaceId } },
          }),
        'Failed to load auto-action settings',
      );
    },
  });
}

export interface UpdateAutoActionSettingsArgs {
  workspaceId: string;
  patch: PatchAutoActionSettingsInput;
}

/** Mutation to PATCH auto-action settings. */
export function useUpdateAutoActionSettings(): UseMutationResult<
  AutoActionSettings,
  Error,
  UpdateAutoActionSettingsArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      workspaceId,
      patch,
    }: UpdateAutoActionSettingsArgs): Promise<AutoActionSettings> => {
      return apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/auto-action-settings', {
            params: { path: { wsId: workspaceId } },
            body: patch,
          }),
        'Failed to update auto-action settings',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: autoActionSettingsKeys.settings(vars.workspaceId),
      });
    },
  });
}
