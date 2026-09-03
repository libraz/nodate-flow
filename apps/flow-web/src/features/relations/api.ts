/**
 * Relation suggestions feature — query key factory, types, and hooks
 * for AI-detected task relation suggestions (auto-detect via embeddings).
 *
 * Calls go through the typed `@nodate-flow/sdk` so request and response
 * shapes stay aligned with the OpenAPI contract.
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
import { ApiError } from '../../lib/api-error';
import { tasksKeys } from '../tasks/api';

/** Suggested relation kind — narrowed from the SDK's open `string` field. */
export type SuggestedKind = 'blocks' | 'relates' | 'duplicates';

/** Suggestion lifecycle status — narrowed from the SDK's open `string` field. */
export type SuggestionStatus = 'pending' | 'accepted' | 'dismissed';

/** A single AI-detected relation suggestion. */
export interface RelationSuggestion
  extends Omit<components['schemas']['SuggestionDTO'], 'suggestedKind' | 'status'> {
  suggestedKind: SuggestedKind;
  status: SuggestionStatus;
}

/** Action to resolve a suggestion. */
export type ResolveAction = 'accept' | 'dismiss';

/** Args for the resolve mutation. */
export interface ResolveSuggestionArgs {
  suggestionId: string;
  action: ResolveAction;
  /** The task whose suggestion list should be optimistically updated. */
  taskId: string;
}

/** Query key factory for the relation suggestions feature. */
export const relationKeys = {
  all: ['relation-suggestions'] as const,
  forTask: (taskId: string) => [...relationKeys.all, 'task', taskId] as const,
  forWorkspace: (wsId: string) => [...relationKeys.all, 'workspace', wsId] as const,
};

export { ApiError as RelationApiError };

/**
 * GET /tasks/{id}/relation-suggestions — non-suspense query.
 *
 * Returns pending AI-detected relation suggestions for a task. Uses
 * `useQuery` (not suspense) because suggestions are optional and should
 * not block the task detail rendering.
 */
export function useRelationSuggestionsForTask(
  taskId: string,
): UseQueryResult<RelationSuggestion[]> {
  return useQuery({
    queryKey: relationKeys.forTask(taskId),
    queryFn: async (): Promise<RelationSuggestion[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/relation-suggestions', {
            params: { path: { id: taskId } },
          }),
        'Failed to load relation suggestions',
      );
      const list = (data.suggestions ?? []) as RelationSuggestion[];
      return list.filter((s) => s.status === 'pending');
    },
    staleTime: 60_000,
  });
}

/**
 * POST /relation-suggestions/{suggestionId}/resolve — optimistic
 * removal from the task suggestion list on accept or dismiss.
 */
export function useResolveSuggestion(): UseMutationResult<void, ApiError, ResolveSuggestionArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ suggestionId, action }: ResolveSuggestionArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/relation-suggestions/{suggestionId}/resolve', {
            params: { path: { suggestionId } },
            body: { action },
          }),
        'Failed to resolve relation suggestion',
      );
    },
    onMutate: async ({ suggestionId, taskId }) => {
      await qc.cancelQueries({ queryKey: relationKeys.forTask(taskId) });

      const prev = qc.getQueryData<RelationSuggestion[]>(relationKeys.forTask(taskId));

      if (prev) {
        qc.setQueryData(
          relationKeys.forTask(taskId),
          prev.filter((s) => s.id !== suggestionId),
        );
      }

      return { prev };
    },
    onError: (_err, { taskId }, ctx) => {
      const snap = ctx as { prev?: RelationSuggestion[] } | undefined;
      if (snap?.prev !== undefined) {
        qc.setQueryData(relationKeys.forTask(taskId), snap.prev);
      }
    },
    onSettled: (_data, _err, { taskId, action }) => {
      void qc.invalidateQueries({ queryKey: relationKeys.forTask(taskId) });
      // When a suggestion is accepted, the backend creates a real
      // dependency edge — refresh the dependencies list too.
      if (action === 'accept') {
        void qc.invalidateQueries({ queryKey: tasksKeys.dependencies(taskId) });
      }
    },
  });
}
