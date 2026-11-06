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

import { apiRequest } from '../../lib/api';
import { authStore, selectUser, useAuth } from '../auth/auth-store';
import { inboxKeys } from '../inbox/api';
import { useStreamHealthy } from '../realtime/stream-health';

/** AI suggestion summary mirrored from the SDK. */
export type AiSuggestion = components['schemas']['AiSuggestionSummary'];
export type AiSuggestionAction = 'archive' | 'snooze' | 'open';

export interface ExecuteAiSuggestionArgs {
  workspaceId: string;
  actorId: string;
  inboxItemId: string;
  proposalId: string;
  expectedRevision: number;
  operationKey: string;
  action: AiSuggestionAction;
  reasoning: string;
}

export interface ExecuteAiSuggestionResult {
  operationId: string;
  proposalId: string;
  revision: number;
  action: string;
  status: string;
  inboxItemId: string;
  taskId?: string;
  snoozeUntil?: number;
  executedAt?: number;
  replayed: boolean;
}

export interface SaveAiSuggestionDecisionArgs {
  workspaceId: string;
  actorId: string;
  inboxItemId: string;
  proposalId: string;
  expectedRevision: number;
  state: 'edited' | 'dismissed' | 'deferred';
  action?: AiSuggestionAction;
  reasoning?: string;
  deferUntil?: number;
}

export interface SaveAiSuggestionDecisionResult {
  proposalId: string;
  revision: number;
  state: string;
  action?: string;
  reasoning?: string;
  decisionExpiresAt: number;
}

/** Thrown before a request when the signed-in actor no longer matches the click. */
export class SuggestionActorChangedError extends Error {
  constructor() {
    super('The signed-in actor changed before the suggestion request started.');
    this.name = 'SuggestionActorChangedError';
  }
}

function assertCurrentActor(actorId: string): void {
  if (authStore.getState().user?.id !== actorId) throw new SuggestionActorChangedError();
}

const POLL_INTERVAL_MS = 30_000;

/** Query key factory for AI suggestions. */
export const aiSuggestionsKeys = {
  all: ['ai-suggestions'] as const,
  listPrefix: (workspaceId: string) => [...aiSuggestionsKeys.all, 'list', workspaceId] as const,
  list: (workspaceId: string, actorId?: string) =>
    [...aiSuggestionsKeys.listPrefix(workspaceId), actorId ?? ''] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as AiSuggestionsApiError };

/** True when the server rejected a command because the proposal moved on. */
function isSuggestionConflict(error: unknown): error is ApiError {
  return (
    error instanceof ApiError &&
    (error.code === 'AI.SUGGESTION.ALREADY_RESOLVED' ||
      error.code === 'AI.SUGGESTION.EVIDENCE_CHANGED')
  );
}

/**
 * useAiSuggestionsQuery — non-suspense list of cross-device AI suggestions
 * for the given workspace. Polls every 30s and tolerates errors silently.
 */
export function useAiSuggestionsQuery(
  workspaceId: string | undefined,
): UseQueryResult<AiSuggestion[], ApiError> {
  const streamHealthy = useStreamHealthy();
  const actorId = useAuth(selectUser)?.id;
  return useQuery<AiSuggestion[], ApiError>({
    queryKey: aiSuggestionsKeys.list(workspaceId ?? '', actorId),
    enabled:
      typeof workspaceId === 'string' &&
      workspaceId.length > 0 &&
      typeof actorId === 'string' &&
      actorId.length > 0,
    refetchInterval: streamHealthy ? false : POLL_INTERVAL_MS,
    staleTime: POLL_INTERVAL_MS,
    retry: false,
    // This panel is decorative; opt out of the SDK-wide `throwOnError: true`
    // default so a 404 / AI-disabled response never cascades to the route
    // ErrorBoundary. Callers render the error state inline.
    throwOnError: false,
    queryFn: async (): Promise<AiSuggestion[]> => {
      if (!workspaceId) return [];
      return fetchAiSuggestions(workspaceId);
    },
  });
}

/** Fetches the persisted proposal list for use after a triage mutation. */
export async function fetchAiSuggestions(workspaceId: string): Promise<AiSuggestion[]> {
  const data = await apiRequest(
    (client) =>
      client.GET('/workspaces/{wsId}/ai/suggestions', {
        params: { path: { wsId: workspaceId } },
      }),
    'Failed to load AI suggestions',
  );
  return data.suggestions ?? [];
}

/** Executes the server-owned effect with a click-captured identity and receipt key. */
export function useExecuteAiSuggestion(): UseMutationResult<
  ExecuteAiSuggestionResult,
  ApiError | SuggestionActorChangedError,
  ExecuteAiSuggestionArgs
> {
  const qc = useQueryClient();
  return useMutation<
    ExecuteAiSuggestionResult,
    ApiError | SuggestionActorChangedError,
    ExecuteAiSuggestionArgs
  >({
    mutationFn: async (input) => {
      assertCurrentActor(input.actorId);
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/ai/suggestions/{inboxItemId}/execute', {
            params: { path: { wsId: input.workspaceId, inboxItemId: input.inboxItemId } },
            body: {
              operationKey: input.operationKey,
              proposalId: input.proposalId,
              expectedRevision: input.expectedRevision,
              action: input.action,
              reasoning: input.reasoning,
            },
          }),
        'Failed to execute AI suggestion',
      );
      return data;
    },
    onSuccess: (_data, input) => {
      void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.listPrefix(input.workspaceId) });
      void qc.invalidateQueries({ queryKey: inboxKeys.list() });
    },
    onError: (error, input) => {
      if (isSuggestionConflict(error)) {
        void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.listPrefix(input.workspaceId) });
      }
      if (error instanceof ApiError && error.code === 'AI.SUGGESTION.ALREADY_RESOLVED') {
        void qc.invalidateQueries({ queryKey: inboxKeys.list() });
      }
    },
  });
}

/** Saves an actor-scoped edit, dismissal, or deferral for one proposal revision. */
export function useSaveAiSuggestionDecision(): UseMutationResult<
  SaveAiSuggestionDecisionResult,
  ApiError | SuggestionActorChangedError,
  SaveAiSuggestionDecisionArgs
> {
  const qc = useQueryClient();
  return useMutation<
    SaveAiSuggestionDecisionResult,
    ApiError | SuggestionActorChangedError,
    SaveAiSuggestionDecisionArgs
  >({
    mutationFn: async (input) => {
      assertCurrentActor(input.actorId);
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/suggestions/{inboxItemId}/decision', {
            params: { path: { wsId: input.workspaceId, inboxItemId: input.inboxItemId } },
            body: {
              proposalId: input.proposalId,
              expectedRevision: input.expectedRevision,
              state: input.state,
              ...(input.action !== undefined ? { action: input.action } : {}),
              ...(input.reasoning !== undefined ? { reasoning: input.reasoning } : {}),
              ...(input.deferUntil !== undefined ? { deferUntil: input.deferUntil } : {}),
            },
          }),
        'Failed to save AI suggestion decision',
      );
      return data;
    },
    onSuccess: (_data, input) => {
      void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.listPrefix(input.workspaceId) });
    },
    onError: (error, input) => {
      if (isSuggestionConflict(error)) {
        void qc.invalidateQueries({ queryKey: aiSuggestionsKeys.listPrefix(input.workspaceId) });
      }
    },
  });
}
