/**
 * Inbox feature — typed Suspense queries and mutations for the signal-backed
 * inbox. Types are sourced from the generated SDK.
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

/** Source of an inbox signal. Backend is an open string; we narrow for UI. */
export type InboxSource = 'manual' | 'github' | 'slack' | 'email' | 'webhook';

/** Inbox item DTO mirrored from the SDK. `receivedAt` / `createdAt` are unix seconds. */
export type InboxItem = components['schemas']['Item'];

/** Query key factory for the inbox feature. */
export const inboxKeys = {
  all: ['inbox'] as const,
  list: () => [...inboxKeys.all, 'list'] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as InboxApiError };

/** GET /inbox — list items for the caller. */
export function useInboxQuery(): UseSuspenseQueryResult<InboxItem[]> {
  return useSuspenseQuery({
    queryKey: inboxKeys.list(),
    queryFn: async (): Promise<InboxItem[]> => {
      const data = await apiRequest((client) => client.GET('/inbox'), 'Failed to load inbox');
      return data.items ?? [];
    },
  });
}

function removeFromLists(
  qc: ReturnType<typeof useQueryClient>,
  id: string,
): { snapshots: [readonly unknown[], InboxItem[] | undefined][] } {
  const snapshots = qc.getQueriesData<InboxItem[]>({ queryKey: inboxKeys.list() });
  for (const [key, value] of snapshots) {
    if (!value) continue;
    qc.setQueryData(
      key,
      value.filter((item) => item.id !== id),
    );
  }
  return { snapshots };
}

/** POST /inbox/{id}/archive with optimistic list removal. */
export function useArchiveInboxItem(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/inbox/{id}/archive', {
            params: { path: { id } },
          }),
        'Failed to archive inbox item',
      );
    },
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: inboxKeys.list() });
      return removeFromLists(qc, id);
    },
    onError: (_err, _id, ctx) => {
      const snap = ctx as
        | { snapshots: [readonly unknown[], InboxItem[] | undefined][] }
        | undefined;
      if (!snap) return;
      for (const [key, value] of snap.snapshots) {
        qc.setQueryData(key, value);
      }
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: inboxKeys.list() });
    },
  });
}

export interface SnoozeInboxArgs {
  id: string;
  /** Unix seconds at which the item should resurface. */
  snoozeUntil: number;
}

/** POST /inbox/{id}/snooze with optimistic list removal. */
export function useSnoozeInboxItem(): UseMutationResult<void, ApiError, SnoozeInboxArgs> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, snoozeUntil }: SnoozeInboxArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.POST('/inbox/{id}/snooze', {
            params: { path: { id } },
            body: { snoozeUntil },
          }),
        'Failed to snooze inbox item',
      );
    },
    onMutate: async ({ id }) => {
      await qc.cancelQueries({ queryKey: inboxKeys.list() });
      return removeFromLists(qc, id);
    },
    onError: (_err, _vars, ctx) => {
      const snap = ctx as
        | { snapshots: [readonly unknown[], InboxItem[] | undefined][] }
        | undefined;
      if (!snap) return;
      for (const [key, value] of snap.snapshots) {
        qc.setQueryData(key, value);
      }
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: inboxKeys.list() });
    },
  });
}
