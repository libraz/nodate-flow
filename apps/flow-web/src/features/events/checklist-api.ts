/**
 * Event checklist hooks — list, add, update (title / done / order), and
 * delete the checklist items attached to a single calendar event.
 *
 *   - {@link useEventChecklistQuery}                 GET    /events/{evtId}/checklist
 *   - {@link useAddEventChecklistItemMutation}       POST   /events/{evtId}/checklist
 *   - {@link useUpdateEventChecklistItemMutation}    PATCH  /events/{evtId}/checklist/{itemId}
 *   - {@link useDeleteEventChecklistItemMutation}    DELETE /events/{evtId}/checklist/{itemId}
 *
 * All hooks share the `['events', 'checklist', wsId, calId, evtId]`
 * cache row. The update mutation performs an optimistic patch on the
 * cached list so toggling `done` (the most common UX path) feels
 * instant. On error it rolls back and the final invalidation reconciles
 * with the authoritative server state.
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

export type EventChecklistItem = components['schemas']['ChecklistItemResponse'];
export type UpdateChecklistItemBody = components['schemas']['UpdateChecklistItemInputBody'];

export const eventChecklistKeys = {
  list: (wsId: string, calId: string, evtId: string) =>
    ['events', 'checklist', wsId, calId, evtId] as const,
};

/** GET /workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist. */
export function useEventChecklistQuery(
  wsId: string,
  calId: string,
  evtId: string,
): UseSuspenseQueryResult<EventChecklistItem[]> {
  return useSuspenseQuery({
    queryKey: eventChecklistKeys.list(wsId, calId, evtId),
    queryFn: async (): Promise<EventChecklistItem[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist', {
            params: { path: { wsId, calId, evtId } },
          }),
        'Failed to load checklist',
      );
      return data.items ?? [];
    },
  });
}

export interface AddEventChecklistItemArgs {
  wsId: string;
  calId: string;
  evtId: string;
  title: string;
  sortWeight?: number;
}

/** POST /workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist. */
export function useAddEventChecklistItemMutation(): UseMutationResult<
  EventChecklistItem,
  ApiError,
  AddEventChecklistItemArgs
> {
  const qc = useQueryClient();
  return useMutation<EventChecklistItem, ApiError, AddEventChecklistItemArgs>({
    mutationFn: async ({ wsId, calId, evtId, title, sortWeight }): Promise<EventChecklistItem> => {
      const body: components['schemas']['CreateChecklistItemInputBody'] = { title };
      if (sortWeight !== undefined) body.sortWeight = sortWeight;
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist', {
            params: { path: { wsId, calId, evtId } },
            body,
          }),
        'Failed to add checklist item',
      );
      return data;
    },
    onSuccess: (_data, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventChecklistKeys.list(wsId, calId, evtId) });
    },
  });
}

export interface UpdateEventChecklistItemArgs {
  wsId: string;
  calId: string;
  evtId: string;
  itemId: string;
  patch: UpdateChecklistItemBody;
}

/**
 * PATCH /workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}.
 *
 * Performs an optimistic update on the cached list so done-toggles feel
 * instant. Rolls back on error and invalidates on settle so the final
 * state always matches the authoritative server.
 */
export function useUpdateEventChecklistItemMutation(): UseMutationResult<
  void,
  ApiError,
  UpdateEventChecklistItemArgs
> {
  const qc = useQueryClient();
  return useMutation<
    void,
    ApiError,
    UpdateEventChecklistItemArgs,
    { snapshot: EventChecklistItem[] | undefined }
  >({
    mutationFn: async ({ wsId, calId, evtId, itemId, patch }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}', {
            params: { path: { wsId, calId, evtId, itemId } },
            body: patch,
          }),
        'Failed to update checklist item',
      );
    },
    onMutate: async ({ wsId, calId, evtId, itemId, patch }) => {
      const key = eventChecklistKeys.list(wsId, calId, evtId);
      await qc.cancelQueries({ queryKey: key });
      const snapshot = qc.getQueryData<EventChecklistItem[]>(key);
      if (snapshot) {
        const next = snapshot.map((item) => {
          if (item.id !== itemId) return item;
          const merged: EventChecklistItem = { ...item };
          if (patch.title !== undefined) merged.title = patch.title;
          if (patch.done !== undefined) merged.done = patch.done;
          if (patch.sortWeight !== undefined) merged.sortWeight = patch.sortWeight;
          return merged;
        });
        qc.setQueryData(key, next);
      }
      return { snapshot };
    },
    onError: (_err, { wsId, calId, evtId }, ctx) => {
      if (ctx?.snapshot) {
        qc.setQueryData(eventChecklistKeys.list(wsId, calId, evtId), ctx.snapshot);
      }
    },
    onSettled: (_data, _err, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventChecklistKeys.list(wsId, calId, evtId) });
    },
  });
}

export interface DeleteEventChecklistItemArgs {
  wsId: string;
  calId: string;
  evtId: string;
  itemId: string;
}

/** DELETE /workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}. */
export function useDeleteEventChecklistItemMutation(): UseMutationResult<
  void,
  ApiError,
  DeleteEventChecklistItemArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteEventChecklistItemArgs>({
    mutationFn: async ({ wsId, calId, evtId, itemId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}', {
            params: { path: { wsId, calId, evtId, itemId } },
          }),
        'Failed to delete checklist item',
      );
    },
    onSuccess: (_void, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventChecklistKeys.list(wsId, calId, evtId) });
    },
  });
}
