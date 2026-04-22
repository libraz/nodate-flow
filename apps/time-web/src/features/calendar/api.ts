import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { DateTime } from 'luxon';

import { toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';
import { useWorkspace, workspaceStore } from '../../stores/workspace-store';
import { expandAllRecurrences } from './recurrence';
import type { Calendar, CalendarEvent, CalendarMember, Rsvp, SubscriptionRole } from './types';

/**
 * Unwraps an SDK response: returns `data` on success, throws an
 * ApiError on failure. This keeps TanStack Query's error boundary
 * pipeline working as expected.
 */
function unwrap<T>(result: { data?: T; error?: unknown }, fallback: string): T {
  if (result.error || !result.data) {
    throw toApiError(result.error, fallback);
  }
  return result.data;
}

export const calendarKeys = {
  all: ['calendars'] as const,
  lists: () => [...calendarKeys.all, 'list'] as const,
  events: (calendarId: string) => [...calendarKeys.all, calendarId, 'events'] as const,
  eventRange: (start: string, end: string) =>
    [...calendarKeys.all, 'events-range', start, end] as const,
  members: (calendarId: string) => [...calendarKeys.all, calendarId, 'members'] as const,
  invites: (calendarId: string) => [...calendarKeys.all, calendarId, 'invites'] as const,
  comments: (eventId: string) => ['comments', eventId] as const,
  checklist: (eventId: string) => ['checklist', eventId] as const,
};

export function useCalendarsQuery() {
  const wsId = useWorkspace((s) => s.workspaceId);
  return useQuery({
    queryKey: [...calendarKeys.lists(), wsId],
    queryFn: async () => {
      const result = await sdk.GET('/workspaces/{wsId}/calendars', {
        params: { path: { wsId: wsId ?? '' } },
      });
      const body = unwrap(result, 'Failed to fetch calendars') as { calendars: Calendar[] };
      return body.calendars;
    },
    enabled: !!wsId,
  });
}

/**
 * Subscribes the caller to the holiday feed for a country (ISO 3166-1 alpha-2).
 * The backend creates the system calendar on first subscription and
 * tolerates duplicate calls.
 */
export function useSubscribeSystemCalendarMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (country: string) => {
      const wsId = workspaceStore.getState().workspaceId;
      const result = await sdk.POST('/workspaces/{wsId}/calendars/subscribe-system', {
        params: { path: { wsId: wsId ?? '' } },
        body: { country },
      });
      return unwrap(result, 'Failed to subscribe to holidays');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

/**
 * Deletes a calendar. Used here to unsubscribe from a system (holiday) feed;
 * the backend enforces that only owners/admins can delete, but deleting a
 * system calendar removes it for the whole workspace.
 */
export function useDeleteCalendarMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (calendarId: string) => {
      const wsId = workspaceStore.getState().workspaceId;
      const result = await sdk.DELETE('/workspaces/{wsId}/calendars/{calendarId}', {
        params: { path: { wsId: wsId ?? '', calendarId } },
      });
      return unwrap(result, 'Failed to delete calendar');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

/**
 * Per `docs/conventions/api-types.md`, the API returns `*_at` fields as
 * `int64` unix seconds. The frontend carries them as ISO strings (typed
 * on `CalendarEvent`) so display code can use `DateTime.fromISO(...)`.
 * Normalize here, at the boundary, preserving the event's zone so ISO
 * offsets match the event timezone at that instant.
 */
type RawEvent = Omit<CalendarEvent, 'startAt' | 'endAt'> & {
  startAt: number | string;
  endAt: number | string;
};

function toIsoInZone(value: number | string, zone: string | undefined): string {
  if (typeof value === 'number') {
    const dt = zone ? DateTime.fromSeconds(value, { zone }) : DateTime.fromSeconds(value);
    return dt.toISO() ?? '';
  }
  return value;
}

function normalizeEvent(raw: RawEvent): CalendarEvent {
  const zone = raw.timezone || undefined;
  return {
    ...raw,
    startAt: toIsoInZone(raw.startAt, zone),
    endAt: toIsoInZone(raw.endAt, zone),
  };
}

export function useCalendarEventsQuery(start: string, end: string, enabled = true) {
  const wsId = useWorkspace((s) => s.workspaceId);
  return useQuery({
    queryKey: [...calendarKeys.eventRange(start, end), wsId],
    queryFn: async () => {
      const result = await sdk.GET('/workspaces/{wsId}/calendar-events', {
        params: {
          path: { wsId: wsId ?? '' },
          query: { start, end },
        },
      });
      const body = unwrap(result, 'Failed to fetch events') as { events: RawEvent[] };
      const events = body.events.map(normalizeEvent);
      return expandAllRecurrences(events, DateTime.fromISO(start), DateTime.fromISO(end));
    },
    enabled: enabled && !!wsId,
  });
}

interface CreateEventInput {
  calendarId: string;
  title: string;
  allDay: boolean;
  startAt: string;
  endAt: string;
  timezone: string;
  kind?: string | undefined;
  showAs?: string | undefined;
  location?: string | undefined;
  memo?: string | undefined;
}

export function useCreateEventMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateEventInput) => {
      const wsId = workspaceStore.getState().workspaceId;
      const { calendarId, ...body } = input;
      const result = await sdk.POST('/workspaces/{wsId}/calendars/{calendarId}/events', {
        params: { path: { wsId: wsId ?? '', calendarId } },
        body,
      });
      return unwrap(result, 'Failed to create event') as CalendarEvent;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

interface UpdateEventInput {
  eventId: string;
  calendarId: string;
  title?: string | undefined;
  allDay?: boolean | undefined;
  startAt?: string | undefined;
  endAt?: string | undefined;
  timezone?: string | undefined;
  kind?: string | undefined;
  showAs?: string | undefined;
  location?: string | undefined;
  memo?: string | undefined;
  recurrenceExceptions?: string[] | undefined;
}

export function useUpdateEventMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: UpdateEventInput) => {
      const wsId = workspaceStore.getState().workspaceId;
      const { eventId, calendarId, ...body } = input;
      const result = await sdk.PATCH('/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}', {
        params: { path: { wsId: wsId ?? '', calendarId, eventId } },
        body,
      });
      return unwrap(result, 'Failed to update event') as CalendarEvent;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

export function useDeleteEventMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ calendarId, eventId }: { calendarId: string; eventId: string }) => {
      const wsId = workspaceStore.getState().workspaceId;
      const result = await sdk.DELETE(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}',
        {
          params: { path: { wsId: wsId ?? '', calendarId, eventId } },
        },
      );
      if (result.error) throw toApiError(result.error, 'Failed to delete event');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

// --- Members ---

export function useMembersQuery(wsId: string, calendarId: string, enabled = true) {
  return useQuery({
    queryKey: calendarKeys.members(calendarId),
    queryFn: async () => {
      const result = await sdk.GET('/workspaces/{wsId}/calendars/{calendarId}/members', {
        params: { path: { wsId, calendarId } },
      });
      const body = unwrap(result, 'Failed to fetch members') as { members: CalendarMember[] };
      return body.members;
    },
    enabled,
  });
}

export function useAddMemberMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { email: string; role: SubscriptionRole }) => {
      const result = await sdk.POST('/workspaces/{wsId}/calendars/{calendarId}/members', {
        params: { path: { wsId, calendarId } },
        body: input,
      });
      return unwrap(result, 'Failed to add member') as CalendarMember;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.members(calendarId) });
    },
  });
}

export function useUpdateMemberRoleMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { userId: string; role: SubscriptionRole }) => {
      const result = await sdk.PATCH('/workspaces/{wsId}/calendars/{calendarId}/members/{userId}', {
        params: { path: { wsId, calendarId, userId: input.userId } },
        body: { role: input.role },
      });
      if (result.error) throw toApiError(result.error, 'Failed to update member role');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.members(calendarId) });
    },
  });
}

export function useRemoveMemberMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (userId: string) => {
      const result = await sdk.DELETE(
        '/workspaces/{wsId}/calendars/{calendarId}/members/{userId}',
        {
          params: { path: { wsId, calendarId, userId } },
        },
      );
      if (result.error) throw toApiError(result.error, 'Failed to remove member');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.members(calendarId) });
    },
  });
}

// --- Invites ---

interface InviteResponse {
  id: string;
  token: string;
  role: string;
  maxUses?: number;
  useCount: number;
  expiresAt?: string;
  createdAt: string;
}

export function useInvitesQuery(wsId: string, calendarId: string, enabled = true) {
  return useQuery({
    queryKey: calendarKeys.invites(calendarId),
    queryFn: async () => {
      const result = await sdk.GET('/workspaces/{wsId}/calendars/{calendarId}/invites', {
        params: { path: { wsId, calendarId } },
      });
      const body = unwrap(result, 'Failed to fetch invites') as { invites: InviteResponse[] };
      return body.invites;
    },
    enabled,
  });
}

export function useCreateInviteMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { role: string; maxUses?: number; expiresAt?: string }) => {
      const result = await sdk.POST('/workspaces/{wsId}/calendars/{calendarId}/invites', {
        params: { path: { wsId, calendarId } },
        body: input,
      });
      return unwrap(result, 'Failed to create invite') as InviteResponse;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.invites(calendarId) });
    },
  });
}

export function useRevokeInviteMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (inviteId: string) => {
      const result = await sdk.DELETE(
        '/workspaces/{wsId}/calendars/{calendarId}/invites/{inviteId}',
        {
          params: { path: { wsId, calendarId, inviteId } },
        },
      );
      if (result.error) throw toApiError(result.error, 'Failed to revoke invite');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.invites(calendarId) });
    },
  });
}

export function useAcceptInviteMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (token: string) => {
      const result = await sdk.POST('/invites/{token}/accept', {
        params: { path: { token } },
        body: {},
      });
      return unwrap(result, 'Failed to accept invite') as {
        calendarId: string;
        calendarName: string;
        role: string;
      };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

// --- RSVP ---

export function useUpdateRsvpMutation(wsId: string, calendarId: string, eventId: string) {
  return useMutation({
    mutationFn: async (rsvp: Rsvp) => {
      const result = await sdk.PATCH(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}/attendees/rsvp',
        {
          params: { path: { wsId, calendarId, eventId } },
          body: { rsvp },
        },
      );
      if (result.error) throw toApiError(result.error, 'Failed to update RSVP');
    },
  });
}

// --- Comments ---

interface CommentResponse {
  id: string;
  userId: string;
  displayName: string;
  avatarUrl?: string;
  body: string;
  editedAt?: string;
  createdAt: string;
}

export function useCommentsQuery(
  wsId: string,
  calendarId: string,
  eventId: string,
  enabled = true,
) {
  return useQuery({
    queryKey: calendarKeys.comments(eventId),
    queryFn: async () => {
      const result = await sdk.GET(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}/comments',
        {
          params: { path: { wsId, calendarId, eventId } },
        },
      );
      const body = unwrap(result, 'Failed to fetch comments') as {
        comments: CommentResponse[];
      };
      return body.comments;
    },
    enabled,
  });
}

export function useCreateCommentMutation(wsId: string, calendarId: string, eventId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (commentBody: string) => {
      const result = await sdk.POST(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}/comments',
        {
          params: { path: { wsId, calendarId, eventId } },
          body: { body: commentBody },
        },
      );
      return unwrap(result, 'Failed to create comment') as CommentResponse;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.comments(eventId) });
    },
  });
}

// --- Checklist ---

interface ChecklistItemResponse {
  id: string;
  title: string;
  done: boolean;
  sortWeight: number;
  createdAt: string;
}

export function useChecklistQuery(
  wsId: string,
  calendarId: string,
  eventId: string,
  enabled = true,
) {
  return useQuery({
    queryKey: calendarKeys.checklist(eventId),
    queryFn: async () => {
      const result = await sdk.GET(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}/checklist',
        {
          params: { path: { wsId, calendarId, eventId } },
        },
      );
      const body = unwrap(result, 'Failed to fetch checklist') as {
        items: ChecklistItemResponse[];
      };
      return body.items;
    },
    enabled,
  });
}

export function useCreateChecklistItemMutation(wsId: string, calendarId: string, eventId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { title: string; sortWeight?: number }) => {
      const result = await sdk.POST(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}/checklist',
        {
          params: { path: { wsId, calendarId, eventId } },
          body: input,
        },
      );
      return unwrap(result, 'Failed to create checklist item') as ChecklistItemResponse;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.checklist(eventId) });
    },
  });
}

export function useUpdateChecklistItemMutation(wsId: string, calendarId: string, eventId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: {
      itemId: string;
      title?: string;
      done?: boolean;
      sortWeight?: number;
    }) => {
      const result = await sdk.PATCH(
        '/workspaces/{wsId}/calendars/{calendarId}/events/{eventId}/checklist/{itemId}',
        {
          params: { path: { wsId, calendarId, eventId, itemId: input.itemId } },
          body: input,
        },
      );
      if (result.error) throw toApiError(result.error, 'Failed to update checklist item');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.checklist(eventId) });
    },
  });
}
