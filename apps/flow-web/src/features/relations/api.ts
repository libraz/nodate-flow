/**
 * Relation suggestions feature — query key factory, types, and hooks
 * for AI-detected task relation suggestions (auto-detect via embeddings).
 *
 * Uses raw fetch (not the SDK) because these endpoints may not yet be
 * in the generated OpenAPI spec. Follows the same pattern as
 * `features/notifications/api.ts`.
 */

import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';
import { tasksKeys } from '../tasks/api';

/** A single AI-detected relation suggestion. */
export interface RelationSuggestion {
  id: string;
  suggestedKind: 'blocks' | 'relates' | 'duplicates';
  confidence: number;
  status: 'pending' | 'accepted' | 'dismissed';
  sourceTaskId: string;
  sourceTaskTitle: string;
  targetTaskId: string;
  targetTaskTitle: string;
  createdAt: number;
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

import { ApiError, toApiError } from '../../lib/api-error';

export { ApiError as RelationApiError };

function authHeaders(): HeadersInit {
  const token = authStore.getState().accessToken;
  // biome-ignore lint/style/useNamingConvention: HTTP header name
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      ...authHeaders(),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as unknown;
    throw toApiError(body, `Request failed with status ${String(res.status)}`);
  }
  return (await res.json()) as T;
}

/**
 * GET /tasks/{taskId}/relation-suggestions — non-suspense query.
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
      const data = await fetchJson<{ total?: number; suggestions?: RelationSuggestion[] }>(
        `${apiBaseUrl}/tasks/${taskId}/relation-suggestions`,
      );
      return (data.suggestions ?? []).filter((s) => s.status === 'pending');
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
      await fetchJson<unknown>(`${apiBaseUrl}/relation-suggestions/${suggestionId}/resolve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action }),
      });
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
