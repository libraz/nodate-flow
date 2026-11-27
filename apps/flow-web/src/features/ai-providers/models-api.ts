/**
 * Models under an AI provider — typed queries and mutations.
 *
 * The list query is non-suspense and only runs while the caller says the
 * section is open; the available-models query reaches the upstream
 * provider, so it runs only while the add dialog is open.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type QueryClient,
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';
import { agentsKeys } from './agents-api';
import { aiStatusKeys } from './ai-status-api';
import { aiProvidersKeys } from './api';

export type ProviderModel = components['schemas']['ProviderModel'];
export type CreateProviderModelInput = components['schemas']['CreateProviderModelInputBody'];
export type PatchProviderModelInput = components['schemas']['PatchProviderModelInputBody'];

const AVAILABLE_MODELS_STALE_MS = 5 * 60 * 1000;

const NO_MODELS: readonly string[] = [];

/** Query key factory for provider-scoped model queries. */
export const providerModelsKeys = {
  prefix: (workspaceId: string) => ['ai-provider-models', workspaceId] as const,
  list: (workspaceId: string, providerId: string) =>
    ['ai-provider-models', workspaceId, providerId] as const,
  available: (workspaceId: string, providerId: string) =>
    ['ai-provider-available-models', workspaceId, providerId] as const,
};

/** Everything a model change can alter: the provider's list, the picker, the provider rows. */
function invalidateModelChange(qc: QueryClient, workspaceId: string, providerId: string): void {
  void qc.invalidateQueries({ queryKey: providerModelsKeys.list(workspaceId, providerId) });
  void qc.invalidateQueries({ queryKey: ['ai-models', workspaceId] });
  void qc.invalidateQueries({ queryKey: aiProvidersKeys.list(workspaceId) });
  void qc.invalidateQueries({ queryKey: aiStatusKeys.detail(workspaceId) });
}

/** GET /workspaces/{wsId}/ai/providers/{providerId}/models — enabled and disabled rows. */
export function useProviderModelsQuery(
  workspaceId: string,
  providerId: string,
  enabled: boolean,
): UseQueryResult<ProviderModel[], ApiError> {
  return useQuery<ProviderModel[], ApiError>({
    queryKey: providerModelsKeys.list(workspaceId, providerId),
    enabled,
    throwOnError: false,
    queryFn: async () => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/providers/{providerId}/models', {
            params: { path: { wsId: workspaceId, providerId } },
          }),
        'Failed to load provider models',
      );
      return data.models ?? [];
    },
  });
}

/** GET .../available-models — identifiers the provider key can see. */
export function useAvailableModelsQuery(
  workspaceId: string,
  providerId: string,
  enabled: boolean,
): UseQueryResult<readonly string[], ApiError> {
  return useQuery<readonly string[], ApiError>({
    queryKey: providerModelsKeys.available(workspaceId, providerId),
    enabled,
    staleTime: AVAILABLE_MODELS_STALE_MS,
    retry: false,
    throwOnError: false,
    queryFn: async () => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/ai/providers/{providerId}/available-models', {
            params: { path: { wsId: workspaceId, providerId } },
          }),
        'Failed to load available models',
      );
      return data.models ?? NO_MODELS;
    },
  });
}

/** POST .../models — registers (or re-enables) a model. */
export function useCreateProviderModel(
  workspaceId: string,
  providerId: string,
): UseMutationResult<ProviderModel, ApiError, CreateProviderModelInput> {
  const qc = useQueryClient();
  return useMutation<ProviderModel, ApiError, CreateProviderModelInput>({
    mutationFn: (body) =>
      apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/ai/providers/{providerId}/models', {
            params: { path: { wsId: workspaceId, providerId } },
            body,
          }),
        'Failed to add model',
      ),
    onSuccess: () => {
      invalidateModelChange(qc, workspaceId, providerId);
    },
  });
}

export interface PatchProviderModelArgs {
  modelId: string;
  patch: PatchProviderModelInput;
}

/** PATCH .../models/{modelId} — enable, disable or rename. */
export function usePatchProviderModel(
  workspaceId: string,
  providerId: string,
): UseMutationResult<void, ApiError, PatchProviderModelArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, PatchProviderModelArgs>({
    mutationFn: async ({ modelId, patch }) => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/providers/{providerId}/models/{modelId}', {
            params: { path: { wsId: workspaceId, providerId, modelId } },
            body: patch,
          }),
        'Failed to update model',
      );
    },
    onSuccess: () => {
      invalidateModelChange(qc, workspaceId, providerId);
    },
  });
}

/** PUT .../default-model — the model AI features use for this provider. */
export function useSetProviderDefaultModel(
  workspaceId: string,
  providerId: string,
): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, string>({
    mutationFn: async (modelId) => {
      await apiRequest(
        (client) =>
          client.PUT('/workspaces/{wsId}/ai/providers/{providerId}/default-model', {
            params: { path: { wsId: workspaceId, providerId } },
            body: { modelId },
          }),
        'Failed to set default model',
      );
    },
    onSuccess: () => {
      invalidateModelChange(qc, workspaceId, providerId);
    },
  });
}

export interface UpdateAgentModelArgs {
  agentId: string;
  modelId: string;
}

/** PATCH /workspaces/{wsId}/ai/agents/{agentId}/model — repoint an agent. */
export function useUpdateAgentModel(
  workspaceId: string,
): UseMutationResult<void, ApiError, UpdateAgentModelArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, UpdateAgentModelArgs>({
    mutationFn: async ({ agentId, modelId }) => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/agents/{agentId}/model', {
            params: { path: { wsId: workspaceId, agentId } },
            body: { modelId },
          }),
        'Failed to update agent model',
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: agentsKeys.list(workspaceId) });
      void qc.invalidateQueries({ queryKey: providerModelsKeys.prefix(workspaceId) });
    },
  });
}
