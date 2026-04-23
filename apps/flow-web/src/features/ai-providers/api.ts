/**
 * AI providers feature — typed Suspense queries and mutations.
 *
 * Backend never returns plaintext API keys; only `apiKeyMasked`. The plaintext
 * `apiKey` field on Create/Patch bodies is write-only and must be cleared from
 * caller state immediately on success.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type AiProvider = components['schemas']['Provider'];
export type CreateAiProviderInput = components['schemas']['CreateProviderInputBody'];
export type PatchAiProviderInput = components['schemas']['PatchProviderInputBody'];
export type AiProviderKind = CreateAiProviderInput['kind'];

/** Query key factory for the AI providers feature. */
export const aiProvidersKeys = {
  all: ['ai-providers'] as const,
  list: (workspaceId: string) => [...aiProvidersKeys.all, 'list', workspaceId] as const,
};

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as AiProviderApiError };

/**
 * ApiError augmented with the originating HTTP status, so consumers can
 * tell a 403 (member without admin rights) apart from a transient 5xx
 * without parsing `error.code` strings.
 */
export class AiProvidersQueryError extends ApiError {
  readonly status: number;
  constructor(status: number, code: string | undefined, message: string) {
    super(code, message);
    this.name = 'AiProvidersQueryError';
    this.status = status;
  }
}

/**
 * GET /workspaces/{wsId}/ai/providers — masked list.
 *
 * Non-suspense so callers can render a localized inline error card
 * (permission denied, transient failure, ...) instead of letting the
 * thrown ApiError cascade up to the root FatalFallback. The caller is
 * responsible for handling `isLoading` / `isError` states.
 */
export function useAiProvidersQuery(
  workspaceId: string,
): UseQueryResult<AiProvider[], AiProvidersQueryError> {
  return useQuery<AiProvider[], AiProvidersQueryError>({
    queryKey: aiProvidersKeys.list(workspaceId),
    // Opt out of the SDK-wide `throwOnError: true` default so 403 / 404
    // from member-role users is handled inline by the consumer rather
    // than cascading to the root ErrorBoundary.
    throwOnError: false,
    queryFn: async (): Promise<AiProvider[]> => {
      const { data, error, response } = await sdk.GET('/workspaces/{wsId}/ai/providers', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) {
        const base = toApiError(error, 'Failed to load AI providers');
        throw new AiProvidersQueryError(response.status, base.code, base.message);
      }
      return data.providers ?? [];
    },
  });
}

/** POST /workspaces/{wsId}/ai/providers — register a provider with a write-only key. */
export function useCreateAiProvider(
  workspaceId: string,
): UseMutationResult<AiProvider, ApiError, CreateAiProviderInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateAiProviderInput): Promise<AiProvider> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/ai/providers', {
        params: { path: { wsId: workspaceId } },
        body: input,
      });
      if (error || !data) throw toApiError(error, 'Failed to create AI provider');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
    },
  });
}

export interface UpdateAiProviderArgs {
  providerId: string;
  patch: PatchAiProviderInput;
}

/** PATCH /workspaces/{wsId}/ai/providers/{providerId} — rotate the API key. */
export function useUpdateAiProvider(
  workspaceId: string,
): UseMutationResult<void, ApiError, UpdateAiProviderArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ providerId, patch }: UpdateAiProviderArgs): Promise<void> => {
      const { error } = await sdk.PATCH('/workspaces/{wsId}/ai/providers/{providerId}', {
        params: { path: { wsId: workspaceId, providerId } },
        body: patch,
      });
      if (error) throw toApiError(error, 'Failed to update AI provider');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/ai/providers/{providerId}. */
export function useDeleteAiProvider(
  workspaceId: string,
): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (providerId: string): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/ai/providers/{providerId}', {
        params: { path: { wsId: workspaceId, providerId } },
      });
      if (error) throw toApiError(error, 'Failed to delete AI provider');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
    },
  });
}
