/**
 * Event detail hooks — single-event read used by the
 * `/workspaces/{wsId}/calendars/{calId}/events/{evtId}` route. Patch
 * and delete are also exposed so future iterations can wire inline
 * edits without re-binding the cache layer.
 *
 * The cache shape mirrors the calendar-events feature's existing keys
 * so changes here invalidate the calendar grid in lockstep with the
 * detail page's own queries.
 *
 * Cache invalidation policy
 * -------------------------
 * Centralised through the local `invalidate()` helper:
 *
 *   - Update / Delete → invalidate
 *       1. the event detail key,
 *       2. the per-calendar event list (owned by calendar-events),
 *       3. the cross-workspace `me-events` aggregate that drives the
 *          main grid.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

export type Event = components['schemas']['EventResponse'];
export type PatchEventBody = components['schemas']['PatchEventInputBody'];

export const eventDetailKeys = {
  detail: (wsId: string, calId: string, evtId: string) =>
    ['events', 'detail', wsId, calId, evtId] as const,
};

/**
 * Invalidates every cache that should refresh after an event mutation:
 * the detail row, the calendar's event list, and the cross-workspace
 * aggregate so the calendar grid picks up the change in a single tick.
 */
function invalidate(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string,
  calId: string,
  evtId: string,
): void {
  void qc.invalidateQueries({ queryKey: eventDetailKeys.detail(wsId, calId, evtId) });
  void qc.invalidateQueries({ queryKey: ['calendar-events', 'list', wsId, calId] });
  void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
}

/** GET /workspaces/{wsId}/calendars/{calId}/events/{evtId}. */
export function useEventQuery(
  wsId: string,
  calId: string,
  evtId: string,
): UseSuspenseQueryResult<Event> {
  return useSuspenseQuery({
    queryKey: eventDetailKeys.detail(wsId, calId, evtId),
    queryFn: async (): Promise<Event> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/calendars/{calId}/events/{evtId}', {
        params: { path: { wsId, calId, evtId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load event');
      return data;
    },
  });
}

export interface PatchEventArgs {
  wsId: string;
  calId: string;
  evtId: string;
  body: PatchEventBody;
}

export function usePatchEventMutation(): UseMutationResult<Event, ApiError, PatchEventArgs> {
  const qc = useQueryClient();
  return useMutation<Event, ApiError, PatchEventArgs>({
    mutationFn: async ({ wsId, calId, evtId, body }): Promise<Event> => {
      const { data, error } = await sdk.PATCH(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}',
        { params: { path: { wsId, calId, evtId } }, body },
      );
      if (error || !data) throw toApiError(error, 'Failed to update event');
      return data;
    },
    onSuccess: (_data, { wsId, calId, evtId }) => {
      invalidate(qc, wsId, calId, evtId);
    },
  });
}

export interface DeleteEventArgs {
  wsId: string;
  calId: string;
  evtId: string;
}

export function useDeleteEventMutation(): UseMutationResult<void, ApiError, DeleteEventArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteEventArgs>({
    mutationFn: async ({ wsId, calId, evtId }): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/calendars/{calId}/events/{evtId}', {
        params: { path: { wsId, calId, evtId } },
      });
      if (error) throw toApiError(error, 'Failed to delete event');
    },
    onSuccess: (_void, { wsId, calId, evtId }) => {
      invalidate(qc, wsId, calId, evtId);
    },
  });
}
