/**
 * Calendar settings hooks — single-calendar reads + writes used by the
 * Calendar Settings Drawer (W13). Subscription-level state stays in the
 * calendars-rail feature; this module only deals with the calendar
 * resource itself (rename / color / description, and delete).
 *
 *   - {@link useCalendarQuery}              GET    /workspaces/{wsId}/calendars/{calId}
 *   - {@link useUpdateCalendarMutation}     PATCH  /workspaces/{wsId}/calendars/{calId}
 *   - {@link useDeleteCalendarMutation}     DELETE /workspaces/{wsId}/calendars/{calId}
 *   - {@link useCalendarEventCountQuery}    GET    /workspaces/{wsId}/calendars/{calId}/events/count
 *
 * Both writes invalidate the per-workspace calendar list (shared with
 * the rail and the calendar route) plus the cross-workspace event
 * aggregate so the user's main calendar grid refreshes if a delete
 * changes which events should be visible.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { type ApiError, toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

export type Calendar = components['schemas']['CalendarResponse'];
export type PatchCalendarBody = components['schemas']['PatchCalendarInputBody'];

export const calendarSettingsKeys = {
  detail: (wsId: string, calId: string) => ['calendars', 'detail', wsId, calId] as const,
  eventCount: (wsId: string, calId: string) => ['calendars', 'eventCount', wsId, calId] as const,
};

function invalidateCalendarCaches(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string,
  calId: string,
): void {
  void qc.invalidateQueries({ queryKey: calendarSettingsKeys.detail(wsId, calId) });
  void qc.invalidateQueries({ queryKey: ['calendar-events', 'calendars', wsId] });
  void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
  void qc.invalidateQueries({ queryKey: ['calendar', 'discoverable', wsId] });
}

/**
 * GET /workspaces/{wsId}/calendars/{calId} — load a single calendar's
 * full payload for the Settings Drawer. Suspense-backed; the drawer
 * mounts its own boundary so the parent route stays interactive while
 * the panel hydrates.
 */
export function useCalendarQuery(wsId: string, calId: string): UseSuspenseQueryResult<Calendar> {
  return useSuspenseQuery({
    queryKey: calendarSettingsKeys.detail(wsId, calId),
    queryFn: async (): Promise<Calendar> => {
      const { data, error } = await sdk.GET('/workspaces/{wsId}/calendars/{calId}', {
        params: { path: { wsId, calId } },
      });
      if (error || !data) throw toApiError(error, 'Failed to load calendar');
      return data;
    },
  });
}

export interface UpdateCalendarArgs {
  wsId: string;
  calId: string;
  body: PatchCalendarBody;
}

/** PATCH /workspaces/{wsId}/calendars/{calId} — update name / color / description. */
export function useUpdateCalendarMutation(): UseMutationResult<
  Calendar,
  ApiError,
  UpdateCalendarArgs
> {
  const qc = useQueryClient();
  return useMutation<Calendar, ApiError, UpdateCalendarArgs>({
    mutationFn: async ({ wsId, calId, body }): Promise<Calendar> => {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}/calendars/{calId}', {
        params: { path: { wsId, calId } },
        body,
      });
      if (error || !data) throw toApiError(error, 'Failed to update calendar');
      return data;
    },
    onSuccess: (_data, { wsId, calId }) => {
      invalidateCalendarCaches(qc, wsId, calId);
    },
  });
}

export interface DeleteCalendarArgs {
  wsId: string;
  calId: string;
}

/** DELETE /workspaces/{wsId}/calendars/{calId} — destroy the calendar. */
export function useDeleteCalendarMutation(): UseMutationResult<void, ApiError, DeleteCalendarArgs> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteCalendarArgs>({
    mutationFn: async ({ wsId, calId }): Promise<void> => {
      const { error } = await sdk.DELETE('/workspaces/{wsId}/calendars/{calId}', {
        params: { path: { wsId, calId } },
      });
      if (error) throw toApiError(error, 'Failed to delete calendar');
    },
    onSuccess: (_void, { wsId, calId }) => {
      invalidateCalendarCaches(qc, wsId, calId);
    },
  });
}

/**
 * Best-effort count of events currently in a calendar. Used by the
 * delete confirmation modal so the user sees how much will be removed.
 *
 * The dedicated count endpoint is not exposed today, so we approximate
 * by listing events in a wide window (1 year past + 1 year future)
 * and reporting the length. This is a heuristic — a count endpoint
 * would be preferable when it exists.
 */
export function useCalendarEventCountQuery(
  wsId: string,
  calId: string,
  enabled: boolean,
): UseQueryResult<number, ApiError> {
  return useQuery<number, ApiError>({
    queryKey: calendarSettingsKeys.eventCount(wsId, calId),
    enabled: enabled && wsId.length > 0 && calId.length > 0,
    staleTime: 60_000,
    queryFn: async (): Promise<number> => {
      const now = new Date();
      const start = new Date(now);
      start.setFullYear(start.getFullYear() - 1);
      const end = new Date(now);
      end.setFullYear(end.getFullYear() + 1);
      const { data, error } = await sdk.GET('/workspaces/{wsId}/calendars/{calId}/events', {
        params: {
          path: { wsId, calId },
          query: { start: start.toISOString(), end: end.toISOString() },
        },
      });
      if (error || !data) throw toApiError(error, 'Failed to load event count');
      return (data.events ?? []).length;
    },
  });
}
