/**
 * Reactions feature — typed queries and mutations backed by the SDK.
 */

import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

// TODO: Replace with SDK types after `bun run generate:sdk`
// export type Reaction = components['schemas']['Reaction'];
export interface Reaction {
  id: string;
  emoji: string;
  userId: string;
  userDisplayName: string;
  createdAt: number;
}

/** Query key factory for the reactions feature. */
export const reactionsKeys = {
  all: ['reactions'] as const,
  forTask: (taskId: string) => [...reactionsKeys.all, 'task', taskId] as const,
};

export function useTaskReactionsQuery(taskId: string): UseSuspenseQueryResult<Reaction[]> {
  return useSuspenseQuery({
    queryKey: reactionsKeys.forTask(taskId),
    queryFn: async (): Promise<Reaction[]> => {
      const { data, error } = await sdk.GET('/tasks/{id}/reactions', {
        params: { path: { id: taskId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load reactions');
      return (data as { reactions?: Reaction[] }).reactions ?? [];
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
      const { error } = await sdk.POST('/tasks/{id}/reactions', {
        params: { path: { id: taskId } },
        body: { emoji },
      });
      if (error) throw toApiError(error, 'Failed to add reaction');
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
      const { error } = await sdk.DELETE('/tasks/{id}/reactions/{reactionId}', {
        params: { path: { id: taskId, reactionId } },
      });
      if (error) throw toApiError(error, 'Failed to remove reaction');
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: reactionsKeys.forTask(vars.taskId) });
    },
  });
}
