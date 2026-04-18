/**
 * AI suggestions feature — typed query + mutations against the
 * `/workspaces/{wsId}/ai/suggestions` endpoints. Non-suspense so callers
 * (Glass Dock, inbox) degrade gracefully when AI is not configured.
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
import { useStreamHealthy } from '../realtime/stream-health';

/** AI suggestion summary mirrored from the SDK. */
export type AiSuggestion = components['schemas']['AiSuggestionSummary'];

const POLL_INTERVAL_MS = 30_000;

/** Query key factory for AI suggestions. */
export const aiSuggestionsKeys = {
  all: ['ai-suggestions'] as const,
  list: (workspaceId: string) => [...aiSuggestionsKeys.all, 'list', workspaceId] as const,
};

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as AiSuggestionsApiError };

/**
 * useAiSuggestionsQuery — non-suspense list of cross-device AI suggestions
 * for the given workspace. Polls every 30s and tolerates errors silently.
 */
export function useAiSuggestionsQuery(
  workspaceId: string | undefined,
): UseQueryResult<AiSuggestion[], ApiError> {
  const streamHealthy = useStreamHealthy();
  return useQuery<AiSuggestion[], ApiError>({
    queryKey: aiSuggestionsKeys.list(workspaceId ?? ''),
    enabled: typeof workspaceId === 'string' && workspaceId.length > 0,
    refetchInterval: streamHealthy ? false : POLL_INTERVAL_MS,
    staleTime: POLL_INTERVAL_MS,
    retry: false,
    queryFn: async (): Promise<AiSuggestion[]> => {
      if (!workspaceId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/suggestions', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load AI suggestions');
      return data.suggestions ?? [];
    },
  });
}

/**
 * useApplyAiSuggestion — records that a suggestion was accepted.
 * On success invalidates the suggestions list for the workspace.
 */
export function useApplyAiSuggestion(
  workspaceId: string,
): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, string>({
    mutationFn: async (inboxItemId: string): Promise<void> => {
      const { error } = await sdk.POST('/workspaces/{wsId}/ai/suggestions/{inboxItemId}/apply', {
        params: { path: { wsId: workspaceId, inboxItemId } },
      });
      if (error) throw toApiError(error, 'Failed to apply AI suggestion');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.list(workspaceId) });
    },
  });
}

/**
 * useDismissAiSuggestion — records that a suggestion was rejected.
 * On success invalidates the suggestions list for the workspace.
 */
export function useDismissAiSuggestion(
  workspaceId: string,
): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, string>({
    mutationFn: async (inboxItemId: string): Promise<void> => {
      const { error } = await sdk.POST('/workspaces/{wsId}/ai/suggestions/{inboxItemId}/dismiss', {
        params: { path: { wsId: workspaceId, inboxItemId } },
      });
      if (error) throw toApiError(error, 'Failed to dismiss AI suggestion');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.list(workspaceId) });
    },
  });
}
