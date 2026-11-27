/** Workspace embedding policy backed by the AI settings API. */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';
import { ApiError } from '../../lib/api-error';
import { aiStatusKeys } from './ai-status-api';

export type EmbeddingSettings = components['schemas']['EmbeddingSettingsBody'];
export type EmbeddingMode = EmbeddingSettings['mode'];
export type PatchEmbeddingSettings = components['schemas']['PatchEmbeddingSettingsInputBody'];

export class EmbeddingSettingsQueryError extends ApiError {
  readonly status: number;

  constructor(status: number, code: string | undefined, message: string) {
    super(code, message, status);
    this.name = 'EmbeddingSettingsQueryError';
    this.status = status;
  }
}

export const embeddingSettingsKeys = {
  settings: (workspaceId: string) => ['workspaces', workspaceId, 'embedding-settings'] as const,
};

/** GET /workspaces/{wsId}/ai/embedding-settings. */
export function useEmbeddingSettingsQuery(
  workspaceId: string,
): UseQueryResult<EmbeddingSettings, EmbeddingSettingsQueryError> {
  return useQuery<EmbeddingSettings, EmbeddingSettingsQueryError>({
    queryKey: embeddingSettingsKeys.settings(workspaceId),
    throwOnError: false,
    queryFn: async (): Promise<EmbeddingSettings> => {
      try {
        return await apiRequest(
          (client) =>
            client.GET('/workspaces/{wsId}/ai/embedding-settings', {
              params: { path: { wsId: workspaceId } },
            }),
          'Failed to load embedding settings',
        );
      } catch (cause) {
        if (cause instanceof ApiError) {
          throw new EmbeddingSettingsQueryError(cause.httpStatus ?? 0, cause.code, cause.message);
        }
        throw cause;
      }
    },
  });
}

export interface UpdateEmbeddingSettingsArgs {
  workspaceId: string;
  patch: PatchEmbeddingSettings;
}

/** PATCH /workspaces/{wsId}/ai/embedding-settings. */
export function useUpdateEmbeddingSettings(): UseMutationResult<
  EmbeddingSettings,
  ApiError,
  UpdateEmbeddingSettingsArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, patch }: UpdateEmbeddingSettingsArgs) => {
      return apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/embedding-settings', {
            params: { path: { wsId: workspaceId } },
            body: patch,
          }),
        'Failed to update embedding settings',
      );
    },
    onSuccess: (data, vars) => {
      qc.setQueryData(embeddingSettingsKeys.settings(vars.workspaceId), data);
      void qc.invalidateQueries({ queryKey: aiStatusKeys.detail(vars.workspaceId) });
    },
  });
}
