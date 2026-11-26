/**
 * Calendar label hooks — react-query bindings for the per-calendar
 * label endpoints behind the settings drawer's "Labels" tab.
 *
 * A label write changes what events look like, so every mutation
 * invalidates the label list and the three event caches: the per-calendar
 * lists, the cross-workspace `me-events` aggregate the grid reads, and
 * the event detail rows the dialog reads. The last two do not sit under
 * `calendarEventKeys.all`.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';
import { calendarEventKeys, eventDetailKeys } from '../events/api';

export type CalendarLabel = components['schemas']['CalendarLabelResponse'];

export const calendarLabelKeys = {
  list: (wsId: string, calId: string) => ['calendars', 'labels', wsId, calId] as const,
};

function invalidate(qc: ReturnType<typeof useQueryClient>, wsId: string, calId: string): void {
  void qc.invalidateQueries({ queryKey: calendarLabelKeys.list(wsId, calId) });
  void qc.invalidateQueries({ queryKey: calendarEventKeys.all });
  void qc.invalidateQueries({ queryKey: calendarEventKeys.meEvents });
  void qc.invalidateQueries({ queryKey: eventDetailKeys.all });
}

/** GET /workspaces/{wsId}/calendars/{calId}/labels */
export function useCalendarLabelsQuery(
  wsId: string,
  calId: string,
): UseQueryResult<CalendarLabel[], ApiError> {
  return useQuery<CalendarLabel[], ApiError>({
    queryKey: calendarLabelKeys.list(wsId, calId),
    queryFn: async (): Promise<CalendarLabel[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/labels', {
            params: { path: { wsId, calId } },
          }),
        'Failed to load calendar labels',
      );
      return data.labels ?? [];
    },
  });
}

export interface CreateCalendarLabelArgs {
  wsId: string;
  calId: string;
  name: string;
  color: string;
}

export function useCreateCalendarLabelMutation(): UseMutationResult<
  CalendarLabel,
  ApiError,
  CreateCalendarLabelArgs
> {
  const qc = useQueryClient();
  return useMutation<CalendarLabel, ApiError, CreateCalendarLabelArgs>({
    mutationFn: async ({ wsId, calId, name, color }): Promise<CalendarLabel> =>
      apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/labels', {
            params: { path: { wsId, calId } },
            body: { name, color },
          }),
        'Failed to create calendar label',
      ),
    onSuccess: (_data, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}

export interface UpdateCalendarLabelArgs {
  wsId: string;
  calId: string;
  labelId: string;
  /** Only the fields that changed. */
  body: { name?: string; color?: string };
}

export function useUpdateCalendarLabelMutation(): UseMutationResult<
  CalendarLabel,
  ApiError,
  UpdateCalendarLabelArgs
> {
  const qc = useQueryClient();
  return useMutation<CalendarLabel, ApiError, UpdateCalendarLabelArgs>({
    mutationFn: async ({ wsId, calId, labelId, body }): Promise<CalendarLabel> =>
      apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/labels/{labelId}', {
            params: { path: { wsId, calId, labelId } },
            body,
          }),
        'Failed to update calendar label',
      ),
    onSuccess: (_data, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}

export interface DeleteCalendarLabelArgs {
  wsId: string;
  calId: string;
  labelId: string;
}

export function useDeleteCalendarLabelMutation(): UseMutationResult<
  void,
  ApiError,
  DeleteCalendarLabelArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteCalendarLabelArgs>({
    mutationFn: async ({ wsId, calId, labelId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/labels/{labelId}', {
            params: { path: { wsId, calId, labelId } },
          }),
        'Failed to delete calendar label',
      );
    },
    onSuccess: (_void, { wsId, calId }) => {
      invalidate(qc, wsId, calId);
    },
  });
}
