/**
 * Event invites hooks — list and revoke magic-link invites attached to
 * a calendar event. The create endpoint lives in the calendar-events
 * feature (`useCreateAttendeeInviteMutation`) because issuing an
 * invite is anchored on a specific attendee row; this module covers
 * the per-event aggregate that the Invites tab renders.
 *
 *   - {@link useEventInvitesQuery}             GET    /events/{evtId}/invites
 *   - {@link useRevokeEventInviteMutation}     DELETE /events/{evtId}/invites/{inviteId}
 *
 * Both reads and writes share the `['events', 'invites', wsId, calId, evtId]`
 * cache row; revoking invalidates it so the list collapses without a
 * round-trip.
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

export type EventInvite = components['schemas']['InviteSummaryResponse'];

export const eventInviteKeys = {
  list: (wsId: string, calId: string, evtId: string) =>
    ['events', 'invites', wsId, calId, evtId] as const,
};

/** GET /workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites. */
export function useEventInvitesQuery(
  wsId: string,
  calId: string,
  evtId: string,
): UseSuspenseQueryResult<EventInvite[]> {
  return useSuspenseQuery({
    queryKey: eventInviteKeys.list(wsId, calId, evtId),
    queryFn: async (): Promise<EventInvite[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites', {
            params: { path: { wsId, calId, evtId } },
          }),
        'Failed to load invites',
      );
      return data.invites ?? [];
    },
  });
}

export interface RevokeEventInviteArgs {
  wsId: string;
  calId: string;
  evtId: string;
  inviteId: string;
}

export function useRevokeEventInviteMutation(): UseMutationResult<
  void,
  ApiError,
  RevokeEventInviteArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, RevokeEventInviteArgs>({
    mutationFn: async ({ wsId, calId, evtId, inviteId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites/{inviteId}', {
            params: { path: { wsId, calId, evtId, inviteId } },
          }),
        'Failed to revoke invite',
      );
    },
    onSuccess: (_void, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventInviteKeys.list(wsId, calId, evtId) });
    },
  });
}
