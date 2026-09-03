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

import { apiRequest } from '../../lib/api';

export type AiProvider = components['schemas']['Provider'];
export type CreateAiProviderInput = components['schemas']['CreateProviderInputBody'];
export type PatchAiProviderInput = components['schemas']['PatchProviderInputBody'];
export type AiProviderKind = CreateAiProviderInput['kind'];

/** Query key factory for the AI providers feature. */
export const aiProvidersKeys = {
  all: ['ai-providers'] as const,
  list: (workspaceId: string) => [...aiProvidersKeys.all, 'list', workspaceId] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as AiProviderApiError };

/**
 * ApiError augmented with the originating HTTP status, so consumers can
 * tell a 403 (member without admin rights) apart from a transient 5xx
 * without parsing `error.code` strings.
 */
export class AiProvidersQueryError extends ApiError {
  readonly status: number;
  constructor(status: number, code: string | undefined, message: string) {
    // The status is forwarded to the base class as well: consumers that
    // only know about `ApiError` — the global auth handler, the
    // network-failure heuristic — read `httpStatus`, and an error that
    // hides its status from them reads as a connection problem.
    super(code, message, status);
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
      try {
        const data = await apiRequest(
          (client) =>
            client.GET('/workspaces/{wsId}/ai/providers', {
              params: { path: { wsId: workspaceId } },
            }),
          'Failed to load AI providers',
        );
        return data.providers ?? [];
      } catch (cause) {
        if (cause instanceof ApiError) {
          throw new AiProvidersQueryError(cause.httpStatus ?? 0, cause.code, cause.message);
        }
        throw cause;
      }
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
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/ai/providers', {
            params: { path: { wsId: workspaceId } },
            body: input,
          }),
        'Failed to create AI provider',
      );
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
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/providers/{providerId}', {
            params: { path: { wsId: workspaceId, providerId } },
            body: patch,
          }),
        'Failed to update AI provider',
      );
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
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/ai/providers/{providerId}', {
            params: { path: { wsId: workspaceId, providerId } },
          }),
        'Failed to delete AI provider',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
    },
  });
}
