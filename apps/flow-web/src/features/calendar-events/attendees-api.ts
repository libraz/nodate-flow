/**
 * Calendar event attendees — react-query hooks for the per-event attendee
 * roster surfaced inside the unified EventDialog.
 *
 * Mirrors the conventions in {@link ./api.ts}: typed via the SDK's
 * generated `components['schemas']` shapes, errors normalised through
 * {@link toApiError}, mutations invalidate both the per-event attendee
 * list and (for RSVP) the cross-workspace `['calendar', 'me-events']`
 * aggregate so the calendar grid can refresh viewer-state pills.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

/** Attendee row as returned by GET /attendees and POST /attendees. */
export type Attendee = components['schemas']['AttendeeResponse'];

/**
 * RSVP enum. The OpenAPI schema types `rsvp` as a plain `string` on
 * {@link Attendee} (the response model), but the request body
 * `UpdateRsvpInputBody` constrains it to the four canonical values; we
 * treat both as the same closed set on the client.
 */
export type Rsvp = 'pending' | 'accepted' | 'declined' | 'tentative';

/** Path scope shared by every attendee endpoint. */
export interface AttendeesScope {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

/** Build the canonical query key for an event's attendee list. */
function attendeesQueryKey(
  workspaceId: string,
  calendarId: string,
  eventId: string,
): readonly ['calendar-events', 'attendees', string, string, string] {
  return ['calendar-events', 'attendees', workspaceId, calendarId, eventId] as const;
}

/**
 * useAttendeesQuery — list attendees for a single event.
 *
 * Disabled while any of the path ids is empty so we never hit
 * `/workspaces//calendars//events//attendees`. Callers can treat
 * `data` as `undefined` while loading.
 */
export function useAttendeesQuery({
  workspaceId,
  calendarId,
  eventId,
}: AttendeesScope): UseQueryResult<Attendee[], ApiError> {
  return useQuery<Attendee[], ApiError>({
    queryKey: attendeesQueryKey(workspaceId, calendarId, eventId),
    enabled: workspaceId.length > 0 && calendarId.length > 0 && eventId.length > 0,
    queryFn: async (): Promise<Attendee[]> => {
      const { data, error } = await sdk.GET(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees',
        {
          params: { path: { wsId: workspaceId, calId: calendarId, evtId: eventId } },
        },
      );
      if (error || !data) throw toApiError(error, 'Failed to load attendees');
      return data.attendees ?? [];
    },
  });
}

/** Invalidate the per-event attendee list. */
function invalidateAttendees(qc: ReturnType<typeof useQueryClient>, scope: AttendeesScope): void {
  void qc.invalidateQueries({
    queryKey: attendeesQueryKey(scope.workspaceId, scope.calendarId, scope.eventId),
  });
}

/**
 * useAddAttendeesMutation — POST attendee rows for a list of user public
 * IDs. Returns the freshly-created attendee rows on success.
 */
export function useAddAttendeesMutation(): UseMutationResult<
  Attendee[],
  ApiError,
  AttendeesScope & { userIds: string[] }
> {
  const qc = useQueryClient();
  return useMutation<Attendee[], ApiError, AttendeesScope & { userIds: string[] }>({
    mutationFn: async ({ workspaceId, calendarId, eventId, userIds }): Promise<Attendee[]> => {
      const { data, error } = await sdk.POST(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees',
        {
          params: { path: { wsId: workspaceId, calId: calendarId, evtId: eventId } },
          body: { userIds },
        },
      );
      if (error || !data) throw toApiError(error, 'Failed to add attendees');
      return data.attendees ?? [];
    },
    onSuccess: (_data, vars) => {
      invalidateAttendees(qc, vars);
    },
  });
}

/** useRemoveAttendeeMutation — DELETE a single attendee by user public ID. */
export function useRemoveAttendeeMutation(): UseMutationResult<
  void,
  ApiError,
  AttendeesScope & { userId: string }
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, AttendeesScope & { userId: string }>({
    mutationFn: async ({ workspaceId, calendarId, eventId, userId }): Promise<void> => {
      const { error } = await sdk.DELETE(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}',
        {
          params: {
            path: { wsId: workspaceId, calId: calendarId, evtId: eventId, userId },
          },
        },
      );
      if (error) throw toApiError(error, 'Failed to remove attendee');
    },
    onSuccess: (_data, vars) => {
      invalidateAttendees(qc, vars);
    },
  });
}

/**
 * useUpdateOwnRsvpMutation — PATCH the actor's RSVP on an event.
 *
 * Invalidates both the attendee list (so the badge updates) and the
 * `['calendar', 'me-events']` aggregate (so the calendar grid's pill
 * viewer-state can refresh).
 */
export function useUpdateOwnRsvpMutation(): UseMutationResult<
  void,
  ApiError,
  AttendeesScope & { rsvp: Rsvp }
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, AttendeesScope & { rsvp: Rsvp }>({
    mutationFn: async ({ workspaceId, calendarId, eventId, rsvp }): Promise<void> => {
      const { error } = await sdk.PATCH(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/rsvp',
        {
          params: { path: { wsId: workspaceId, calId: calendarId, evtId: eventId } },
          body: { rsvp },
        },
      );
      if (error) throw toApiError(error, 'Failed to update RSVP');
    },
    onSuccess: (_data, vars) => {
      invalidateAttendees(qc, vars);
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
    },
  });
}

/** useToggleCanEditMutation — PATCH the can-edit flag for a given attendee. */
export function useToggleCanEditMutation(): UseMutationResult<
  void,
  ApiError,
  AttendeesScope & { userId: string; canEdit: boolean }
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, AttendeesScope & { userId: string; canEdit: boolean }>({
    mutationFn: async ({ workspaceId, calendarId, eventId, userId, canEdit }): Promise<void> => {
      const { error } = await sdk.PATCH(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}/can-edit',
        {
          params: {
            path: { wsId: workspaceId, calId: calendarId, evtId: eventId, userId },
          },
          body: { canEdit },
        },
      );
      if (error) throw toApiError(error, 'Failed to update can-edit');
    },
    onSuccess: (_data, vars) => {
      invalidateAttendees(qc, vars);
    },
  });
}

/** Result shape returned to callers of {@link useCreateAttendeeInviteMutation}. */
export interface CreateAttendeeInviteResult {
  id: string;
  token: string;
  expiresAt: number;
}

/**
 * useCreateAttendeeInviteMutation — POST a magic-link invite for an
 * attendee. Returns the freshly-issued token + expiry the caller can
 * surface (copy-to-clipboard etc.). Does not invalidate any caches —
 * issuing a new invite does not change the attendee row.
 */
export function useCreateAttendeeInviteMutation(): UseMutationResult<
  CreateAttendeeInviteResult,
  ApiError,
  AttendeesScope & { attendeeId: string; expiresInHours?: number }
> {
  return useMutation<
    CreateAttendeeInviteResult,
    ApiError,
    AttendeesScope & { attendeeId: string; expiresInHours?: number }
  >({
    mutationFn: async ({
      workspaceId,
      calendarId,
      eventId,
      attendeeId,
      expiresInHours,
    }): Promise<CreateAttendeeInviteResult> => {
      const { data, error } = await sdk.POST(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{attendeeId}/invite',
        {
          params: {
            path: { wsId: workspaceId, calId: calendarId, evtId: eventId, attendeeId },
          },
          body: expiresInHours !== undefined ? { expiresInHours } : {},
        },
      );
      if (error || !data) throw toApiError(error, 'Failed to create invite');
      return { id: data.id, token: data.token, expiresAt: data.expiresAt };
    },
  });
}
