/**
 * Event-from-task hook — POSTs
 * `/workspaces/{wsId}/calendars/{calId}/events/from-task` and
 * invalidates the affected calendar caches plus the task timeline so
 * the freshly created event surfaces on both the calendar grid and the
 * task's history feed without a manual refresh.
 *
 * The backend derives the event's start/end from the task's `due_on` /
 * `due_at` (and the actor's resolved timezone) — the request body only
 * carries `taskId` and an optional explicit `timezone` override.
 */

import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '../../../lib/api';
import type { ApiError } from '../../../lib/api-error';
import { timelineKeys } from '../../timeline/api';
import { tasksKeys } from '../api';

export interface CreateEventFromTaskArgs {
  workspaceId: string;
  calendarId: string;
  taskId: string;
  /** Optional IANA timezone override; defaults to the actor's resolved zone. */
  timezone?: string;
}

export interface CreateEventFromTaskResult {
  eventId: string;
}

export function useCreateEventFromTaskMutation(): UseMutationResult<
  CreateEventFromTaskResult,
  ApiError,
  CreateEventFromTaskArgs
> {
  const qc = useQueryClient();
  return useMutation<CreateEventFromTaskResult, ApiError, CreateEventFromTaskArgs>({
    mutationFn: async ({
      workspaceId,
      calendarId,
      taskId,
      timezone,
    }): Promise<CreateEventFromTaskResult> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/events/from-task', {
            params: { path: { wsId: workspaceId, calId: calendarId } },
            body: { taskId, ...(timezone ? { timezone } : {}) },
          }),
        'Failed to create event from task',
      );
      return { eventId: data.id };
    },
    onSuccess: (_data, { workspaceId, taskId }) => {
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
      void qc.invalidateQueries({ queryKey: ['calendar-events', 'calendars', workspaceId] });
      void qc.invalidateQueries({ queryKey: tasksKeys.detail(taskId) });
      void qc.invalidateQueries({ queryKey: [...timelineKeys.all, 'task', taskId] });
    },
  });
}
