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
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
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

/** Lightweight error thrown when the SDK returns an error envelope. */
export class AiProviderApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'AiProviderApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): AiProviderApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new AiProviderApiError(code, message);
  }
  return new AiProviderApiError(undefined, fallback);
}

/** GET /workspaces/{wsId}/ai/providers — masked list. */
export function useAiProvidersQuery(workspaceId: string): UseSuspenseQueryResult<AiProvider[]> {
  return useSuspenseQuery({
    queryKey: aiProvidersKeys.list(workspaceId),
    queryFn: async (): Promise<AiProvider[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/providers', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toError(error, 'Failed to load AI providers');
      return data.providers ?? [];
    },
  });
}

/** POST /workspaces/{wsId}/ai/providers — register a provider with a write-only key. */
export function useCreateAiProvider(
  workspaceId: string,
): UseMutationResult<AiProvider, AiProviderApiError, CreateAiProviderInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateAiProviderInput): Promise<AiProvider> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/ai/providers', {
        params: { path: { wsId: workspaceId } },
        body: input,
      });
      if (error || !data) throw toError(error, 'Failed to create AI provider');
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
): UseMutationResult<void, AiProviderApiError, UpdateAiProviderArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ providerId, patch }: UpdateAiProviderArgs): Promise<void> => {
      const { error } = await sdk.PATCH('/workspaces/{wsId}/ai/providers/{providerId}', {
        params: { path: { wsId: workspaceId, providerId } },
        body: patch,
      });
      if (error) throw toError(error, 'Failed to update AI provider');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
    },
  });
}

/** DELETE /workspaces/{wsId}/ai/providers/{providerId}. */
export function useDeleteAiProvider(
  workspaceId: string,
): UseMutationResult<void, AiProviderApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (providerId: string): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/ai/providers/{providerId}', {
        params: { path: { wsId: workspaceId, providerId } },
      });
      if (error) throw toError(error, 'Failed to delete AI provider');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
    },
  });
}
