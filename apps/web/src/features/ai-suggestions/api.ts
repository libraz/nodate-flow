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

/** AI suggestion summary mirrored from the SDK. */
export type AiSuggestion = components['schemas']['AiSuggestionSummary'];

const POLL_INTERVAL_MS = 30_000;

/** Query key factory for AI suggestions. */
export const aiSuggestionsKeys = {
  all: ['ai-suggestions'] as const,
  list: (workspaceId: string) => [...aiSuggestionsKeys.all, 'list', workspaceId] as const,
};

/** Lightweight error thrown when the SDK returns an error envelope. */
export class AiSuggestionsApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'AiSuggestionsApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): AiSuggestionsApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new AiSuggestionsApiError(code, message);
  }
  return new AiSuggestionsApiError(undefined, fallback);
}

/**
 * useAiSuggestionsQuery — non-suspense list of cross-device AI suggestions
 * for the given workspace. Polls every 30s and tolerates errors silently.
 */
export function useAiSuggestionsQuery(
  workspaceId: string | undefined,
): UseQueryResult<AiSuggestion[], AiSuggestionsApiError> {
  return useQuery<AiSuggestion[], AiSuggestionsApiError>({
    queryKey: aiSuggestionsKeys.list(workspaceId ?? ''),
    enabled: typeof workspaceId === 'string' && workspaceId.length > 0,
    refetchInterval: POLL_INTERVAL_MS,
    staleTime: POLL_INTERVAL_MS,
    retry: false,
    queryFn: async (): Promise<AiSuggestion[]> => {
      if (!workspaceId) return [];
      const { data, error } = await sdk.GET('/workspaces/{wsId}/ai/suggestions', {
        params: { path: { wsId: workspaceId } },
      });
      if (error || !data) throw toError(error, 'Failed to load AI suggestions');
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
): UseMutationResult<void, AiSuggestionsApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, AiSuggestionsApiError, string>({
    mutationFn: async (inboxItemId: string): Promise<void> => {
      const { error } = await sdk.POST('/workspaces/{wsId}/ai/suggestions/{inboxItemId}/apply', {
        params: { path: { wsId: workspaceId, inboxItemId } },
      });
      if (error) throw toError(error, 'Failed to apply AI suggestion');
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
): UseMutationResult<void, AiSuggestionsApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, AiSuggestionsApiError, string>({
    mutationFn: async (inboxItemId: string): Promise<void> => {
      const { error } = await sdk.POST('/workspaces/{wsId}/ai/suggestions/{inboxItemId}/dismiss', {
        params: { path: { wsId: workspaceId, inboxItemId } },
      });
      if (error) throw toError(error, 'Failed to dismiss AI suggestion');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.list(workspaceId) });
    },
  });
}
