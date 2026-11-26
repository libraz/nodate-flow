/**
 * Calendar members hooks — react-query bindings for the per-calendar
 * member CRUD endpoints used by the Calendar Settings Drawer's
 * "Members" tab.
 *
 *   - {@link useCalendarMembersQuery}        GET    /workspaces/{wsId}/calendars/{calId}/members
 *   - {@link useAddCalendarMemberMutation}   POST   .../members
 *   - {@link useUpdateCalendarMemberRoleMutation} PATCH  .../members/{userId}
 *   - {@link useRemoveCalendarMemberMutation}      DELETE .../members/{userId}
 *   - {@link useUpdateMemberColor}           PUT    .../members/{userId}/color
 *
 * Cache invalidation policy
 * -------------------------
 * Centralised through the local `invalidate()` helper:
 *
 *   - Add / Update role / Remove → invalidate
 *       1. the per-calendar members list,
 *       2. the per-workspace calendar list owned by `calendar-events`,
 *       3. the cross-workspace `me-events` aggregate that drives the
 *          main grid,
 *       4. the per-workspace `discoverable` list shown by the rail.
 *
 * The rail depends on membership for the leave action's eligibility
 * and the grid depends on it for "should this member's events be
 * visible". This is intentionally broader than the members list alone.
 */

import type { components } from '@nodate-flow/sdk';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';
import { calendarEventKeys, eventDetailKeys } from '../events/api';

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
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/members', {
            params: { path: { wsId, calId } },
          }),
        'Failed to load calendar members',
      );
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
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/members', {
            params: { path: { wsId, calId } },
            body: { email, role },
          }),
        'Failed to add member',
      );
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
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/members/{userId}', {
            params: { path: { wsId, calId, userId } },
            body: { role },
          }),
        'Failed to update member role',
      );
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
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/members/{userId}', {
            params: { path: { wsId, calId, userId } },
          }),
        'Failed to remove member',
      );
    },
    onSuccess: (_void, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}

export interface UpdateMemberColorArgs {
  wsId: string;
  calId: string;
  userId: string;
  color: string;
}

interface UpdateMemberColorContext {
  previous: CalendarMember[] | undefined;
}

/**
 * PUT .../members/{userId}/color — set a member's colour. The member list
 * is updated optimistically and restored on failure. Event colours derive
 * from member colours, so a success also refreshes every event cache.
 */
export function useUpdateMemberColor(): UseMutationResult<
  CalendarMember,
  ApiError,
  UpdateMemberColorArgs,
  UpdateMemberColorContext
> {
  const qc = useQueryClient();
  const { t } = useTranslation('common');
  return useMutation<CalendarMember, ApiError, UpdateMemberColorArgs, UpdateMemberColorContext>({
    mutationFn: ({ wsId, calId, userId, color }): Promise<CalendarMember> =>
      apiRequest(
        (client) =>
          client.PUT('/workspaces/{wsId}/calendars/{calId}/members/{userId}/color', {
            params: { path: { wsId, calId, userId } },
            body: { color },
          }),
        'Failed to update member color',
      ),
    onMutate: async ({ wsId, calId, userId, color }) => {
      const key = calendarMembersKeys.list(wsId, calId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<CalendarMember[]>(key);
      qc.setQueryData<CalendarMember[]>(key, (list) =>
        list?.map((m) => (m.userId === userId ? { ...m, memberColor: color } : m)),
      );
      return { previous };
    },
    onError: (_err, { wsId, calId }, context) => {
      qc.setQueryData(calendarMembersKeys.list(wsId, calId), context?.previous);
      toaster.show({ tone: 'danger', message: t('calendar.settings.members.color_error') });
    },
    onSuccess: (_data, { wsId, calId }) => {
      void qc.invalidateQueries({ queryKey: calendarMembersKeys.list(wsId, calId) });
      void qc.invalidateQueries({ queryKey: calendarEventKeys.all });
      void qc.invalidateQueries({ queryKey: calendarEventKeys.meEvents });
      void qc.invalidateQueries({ queryKey: eventDetailKeys.all });
    },
  });
}
