/**
 * Calendar members hooks — react-query bindings for the per-calendar
 * member CRUD endpoints used by the Calendar Settings Drawer's
 * "Members" tab.
 *
 *   - {@link useCalendarMembersQuery}        GET    /workspaces/{wsId}/calendars/{calId}/members
 *   - {@link useAddCalendarMemberMutation}   POST   .../members
 *   - {@link useUpdateCalendarMemberRoleMutation} PATCH  .../members/{userId}
 *   - {@link useRemoveCalendarMemberMutation}      DELETE .../members/{userId}
 *
 * Mutations invalidate the calendar members list and the calendars-rail
 * caches (the rail depends on membership for the leave action's
 * eligibility), plus the cross-workspace event aggregate so the grid
 * refreshes if a removed member's events should disappear.
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

export type CalendarMember = components['schemas']['MemberResponse'];

/** Roles assignable on add. The backend rejects `owner` from this endpoint. */
export type AddableRole = 'manager' | 'editor' | 'viewer';

/** Roles assignable on update. */
export type UpdatableRole = 'owner' | 'manager' | 'editor' | 'viewer';

export const calendarMembersKeys = {
  list: (wsId: string, calId: string) => ['calendars', 'members', wsId, calId] as const,
};

function invalidate(qc: ReturnType<typeof useQueryClient>, wsId: string, calId: string): void {
  void qc.invalidateQueries({ queryKey: calendarMembersKeys.list(wsId, calId) });
  void qc.invalidateQueries({ queryKey: ['calendar-events', 'calendars', wsId] });
  void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
  void qc.invalidateQueries({ queryKey: ['calendar', 'discoverable', wsId] });
}

/**
 * GET /workspaces/{wsId}/calendars/{calId}/members — load the full
 * member list. Suspense-backed so the Members tab can render its own
 * skeleton once and reuse the cache across re-opens.
 */
export function useCalendarMembersQuery(
  wsId: string,
  calId: string,
): UseSuspenseQueryResult<CalendarMember[]> {
  return useSuspenseQuery({
    queryKey: calendarMembersKeys.list(wsId, calId),
    queryFn: async (): Promise<CalendarMember[]> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/calendars/{calId}/members', {
        params: { path: { wsId, calId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load calendar members');
      return data.members ?? [];
    },
  });
}

export interface AddCalendarMemberArgs {
  wsId: string;
  calId: string;
  email: string;
  role: AddableRole;
}

export function useAddCalendarMemberMutation(): UseMutationResult<
  CalendarMember,
  ApiError,
  AddCalendarMemberArgs
> {
  const qc = useQueryClient();
  return useMutation<CalendarMember, ApiError, AddCalendarMemberArgs>({
    mutationFn: async ({ wsId, calId, email, role }): Promise<CalendarMember> => {
      const { data, error } = await sdk.POST('/workspaces/{wsId}/calendars/{calId}/members', {
        params: { path: { wsId, calId } },
        body: { email, role },
      });
      if (error || !data) throw toApiError(error, 'Failed to add member');
      return data;
    },
    onSuccess: (_data, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}

export interface UpdateCalendarMemberRoleArgs {
  wsId: string;
  calId: string;
  userId: string;
  role: UpdatableRole;
}

export function useUpdateCalendarMemberRoleMutation(): UseMutationResult<
  void,
  ApiError,
  UpdateCalendarMemberRoleArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, UpdateCalendarMemberRoleArgs>({
    mutationFn: async ({ wsId, calId, userId, role }): Promise<void> => {
      const { error } = await sdk.PATCH('/workspaces/{wsId}/calendars/{calId}/members/{userId}', {
        params: { path: { wsId, calId, userId } },
        body: { role },
      });
      if (error) throw toApiError(error, 'Failed to update member role');
    },
    onSuccess: (_void, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}

export interface RemoveCalendarMemberArgs {
  wsId: string;
  calId: string;
  userId: string;
}

export function useRemoveCalendarMemberMutation(): UseMutationResult<
  void,
  ApiError,
  RemoveCalendarMemberArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, RemoveCalendarMemberArgs>({
    mutationFn: async ({ wsId, calId, userId }): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/calendars/{calId}/members/{userId}', {
        params: { path: { wsId, calId, userId } },
      });
      if (error) throw toApiError(error, 'Failed to remove member');
    },
    onSuccess: (_void, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}
