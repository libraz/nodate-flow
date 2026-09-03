/**
 * Reactions feature — typed queries and mutations backed by the SDK.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';

export type Reaction = components['schemas']['Reaction'];

/** Query key factory for the reactions feature. */
export const reactionsKeys = {
  all: ['reactions'] as const,
  forTask: (taskId: string) => [...reactionsKeys.all, 'task', taskId] as const,
};

export function useTaskReactionsQuery(taskId: string): UseSuspenseQueryResult<Reaction[]> {
  return useSuspenseQuery({
    queryKey: reactionsKeys.forTask(taskId),
    queryFn: async (): Promise<Reaction[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/tasks/{id}/reactions', {
            params: { path: { id: taskId } },
          }),
        'Failed to load reactions',
      );
      return data.reactions ?? [];
    },
  });
}

export interface AddReactionArgs {
  taskId: string;
  emoji: string;
}

export function useAddReaction(): UseMutationResult<void, ApiError, AddReactionArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, emoji }: AddReactionArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/tasks/{id}/reactions', {
            params: { path: { id: taskId } },
            body: { emoji },
          }),
        'Failed to add reaction',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: reactionsKeys.forTask(vars.taskId) });
    },
  });
}

export interface RemoveReactionArgs {
  taskId: string;
  reactionId: string;
}

export function useRemoveReaction(): UseMutationResult<void, ApiError, RemoveReactionArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, reactionId }: RemoveReactionArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/tasks/{id}/reactions/{reactionId}', {
            params: { path: { id: taskId, reactionId } },
          }),
        'Failed to remove reaction',
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: reactionsKeys.forTask(vars.taskId) });
    },
  });
}
