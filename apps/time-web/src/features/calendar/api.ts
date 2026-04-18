import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { DateTime } from 'luxon';

import { api } from '../../lib/api-client';
import { useWorkspaceStore } from '../../stores/workspace-store';
import { expandAllRecurrences } from './recurrence';
import type { Calendar, CalendarEvent, CalendarMember, Rsvp, SubscriptionRole } from './types';

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
  const wsId = useWorkspaceStore((s) => s.workspaceId);
  return useQuery({
    queryKey: [...calendarKeys.lists(), wsId],
    queryFn: () =>
      api.get<{ calendars: Calendar[] }>(`/workspaces/${wsId}/calendars`).then((r) => r.calendars),
    enabled: !!wsId,
  });
}

export function useCalendarEventsQuery(start: string, end: string, enabled = true) {
  const wsId = useWorkspaceStore((s) => s.workspaceId);
  return useQuery({
    queryKey: [...calendarKeys.eventRange(start, end), wsId],
    queryFn: () =>
      api
        .get<{ events: CalendarEvent[] }>(
          `/workspaces/${wsId}/calendar-events?start=${start}&end=${end}`,
        )
        .then((r) => r.events)
        .then((items) =>
          expandAllRecurrences(items, DateTime.fromISO(start), DateTime.fromISO(end)),
        ),
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
    mutationFn: (input: CreateEventInput) => {
      const wsId = useWorkspaceStore.getState().workspaceId;
      const { calendarId, ...body } = input;
      return api.post<CalendarEvent>(`/workspaces/${wsId}/calendars/${calendarId}/events`, body);
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
    mutationFn: (input: UpdateEventInput) => {
      const wsId = useWorkspaceStore.getState().workspaceId;
      const { eventId, calendarId, ...body } = input;
      return api.patch<CalendarEvent>(
        `/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}`,
        body,
      );
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

export function useDeleteEventMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ calendarId, eventId }: { calendarId: string; eventId: string }) => {
      const wsId = useWorkspaceStore.getState().workspaceId;
      return api.delete(`/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}`);
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
    queryFn: () =>
      api
        .get<{ members: CalendarMember[] }>(`/workspaces/${wsId}/calendars/${calendarId}/members`)
        .then((r) => r.members),
    enabled,
  });
}

export function useAddMemberMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { email: string; role: SubscriptionRole }) =>
      api.post<CalendarMember>(`/workspaces/${wsId}/calendars/${calendarId}/members`, input),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.members(calendarId) });
    },
  });
}

export function useUpdateMemberRoleMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { userId: string; role: SubscriptionRole }) =>
      api.patch(`/workspaces/${wsId}/calendars/${calendarId}/members/${input.userId}`, {
        role: input.role,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.members(calendarId) });
    },
  });
}

export function useRemoveMemberMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      api.delete(`/workspaces/${wsId}/calendars/${calendarId}/members/${userId}`),
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
    queryFn: () =>
      api
        .get<{ invites: InviteResponse[] }>(`/workspaces/${wsId}/calendars/${calendarId}/invites`)
        .then((r) => r.invites),
    enabled,
  });
}

export function useCreateInviteMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { role: string; maxUses?: number; expiresAt?: string }) =>
      api.post<InviteResponse>(`/workspaces/${wsId}/calendars/${calendarId}/invites`, input),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.invites(calendarId) });
    },
  });
}

export function useRevokeInviteMutation(wsId: string, calendarId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (inviteId: string) =>
      api.delete(`/workspaces/${wsId}/calendars/${calendarId}/invites/${inviteId}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.invites(calendarId) });
    },
  });
}

export function useAcceptInviteMutation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (token: string) =>
      api.post<{ calendarId: string; calendarName: string; role: string }>(
        `/invites/${token}/accept`,
        {},
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.all });
    },
  });
}

// --- RSVP ---

export function useUpdateRsvpMutation(wsId: string, calendarId: string, eventId: string) {
  return useMutation({
    mutationFn: (rsvp: Rsvp) =>
      api.patch(`/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}/attendees/rsvp`, {
        rsvp,
      }),
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
    queryFn: () =>
      api
        .get<{ comments: CommentResponse[] }>(
          `/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}/comments`,
        )
        .then((r) => r.comments),
    enabled,
  });
}

export function useCreateCommentMutation(wsId: string, calendarId: string, eventId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: string) =>
      api.post<CommentResponse>(
        `/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}/comments`,
        { body },
      ),
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
    queryFn: () =>
      api
        .get<{ items: ChecklistItemResponse[] }>(
          `/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}/checklist`,
        )
        .then((r) => r.items),
    enabled,
  });
}

export function useCreateChecklistItemMutation(wsId: string, calendarId: string, eventId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { title: string; sortWeight?: number }) =>
      api.post<ChecklistItemResponse>(
        `/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}/checklist`,
        input,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.checklist(eventId) });
    },
  });
}

export function useUpdateChecklistItemMutation(wsId: string, calendarId: string, eventId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      itemId: string;
      title?: string;
      done?: boolean;
      sortWeight?: number;
    }) =>
      api.patch(
        `/workspaces/${wsId}/calendars/${calendarId}/events/${eventId}/checklist/${input.itemId}`,
        input,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: calendarKeys.checklist(eventId) });
    },
  });
}
