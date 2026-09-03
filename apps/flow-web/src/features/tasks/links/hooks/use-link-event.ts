/**
 * useLinkEvent — POST `/tasks/{id}/links` with optimistic insertion.
 *
 * The mutation pre-pends a synthetic `TaskEventLink` to the cached list
 * so the row appears immediately after the picker confirms the choice.
 * On success the server-issued link replaces the optimistic entry; on
 * failure the cache is rolled back and the caller can show the
 * `linkFailed` / `alreadyLinkedError` toast.
 */

import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '../../../../lib/api';
import type { ApiError } from '../../../../lib/api-error';
import type { LinkKind, LinkRelation, TaskEventLink } from '../types';
import { type LinkedEventsResult, linkedEventsKeys } from './use-linked-events';

export interface LinkEventArgs {
  taskId: string;
  eventId: string;
  kind: LinkKind;
  /**
   * Optional client-known event metadata for optimistic rendering. The
   * row is only ever shown for ~250ms before the mutation settles, so a
   * partial snapshot is fine; missing fields fall back to placeholders.
   */
  preview?: {
    title: string;
    calendarId?: string;
    eventStartAt?: number;
    eventEndAt?: number;
    eventAllDay?: boolean;
  };
}

/**
 * Optimistic POST. The optimistic id is a UUID-shaped sentinel
 * (`optimistic-…`) that the row component recognises so it can suppress
 * unlink interactions until the server returns the real id.
 */
export function useLinkEvent(): UseMutationResult<
  TaskEventLink,
  ApiError,
  LinkEventArgs,
  { previous: LinkedEventsResult | undefined; optimisticId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, eventId, kind }: LinkEventArgs): Promise<TaskEventLink> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/tasks/{id}/links', {
            params: { path: { id: taskId } },
            body: { eventId, relation: kind as LinkRelation },
          }),
        'Failed to link event',
      );
      return data;
    },
    onMutate: async ({ taskId, eventId, kind, preview }) => {
      const key = linkedEventsKeys.list(taskId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<LinkedEventsResult>(key);
      const optimisticId = `optimistic-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      const optimistic: TaskEventLink = {
        id: optimisticId,
        relation: kind,
        sortWeight: 0,
        createdAt: Math.floor(Date.now() / 1000),
        eventId,
        ...(preview?.title !== undefined ? { eventTitle: preview.title } : {}),
        ...(preview?.calendarId !== undefined ? { calendarId: preview.calendarId } : {}),
        ...(preview?.eventStartAt !== undefined ? { eventStartAt: preview.eventStartAt } : {}),
        ...(preview?.eventEndAt !== undefined ? { eventEndAt: preview.eventEndAt } : {}),
        ...(preview?.eventAllDay !== undefined ? { eventAllDay: preview.eventAllDay } : {}),
      };
      const next: LinkedEventsResult = {
        links: previous ? [optimistic, ...previous.links] : [optimistic],
        total: (previous?.total ?? 0) + 1,
      };
      qc.setQueryData<LinkedEventsResult>(key, next);
      return { previous, optimisticId };
    },
    onError: (_err, vars, ctx) => {
      if (ctx?.previous !== undefined) {
        qc.setQueryData(linkedEventsKeys.list(vars.taskId), ctx.previous);
      }
    },
    onSuccess: (data, vars, ctx) => {
      const key = linkedEventsKeys.list(vars.taskId);
      const current = qc.getQueryData<LinkedEventsResult>(key);
      if (!current) return;
      // Replace the optimistic placeholder with the server's link.
      const links = current.links.map((l) => (l.id === ctx.optimisticId ? data : l));
      qc.setQueryData<LinkedEventsResult>(key, { ...current, links });
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: linkedEventsKeys.list(vars.taskId) });
    },
  });
}
