/**
 * Event comments hooks — list, add, edit, and delete the threaded
 * discussion attached to a single calendar event.
 *
 *   - {@link useEventCommentsQuery}            GET    /events/{evtId}/comments
 *   - {@link useAddEventCommentMutation}       POST   /events/{evtId}/comments
 *   - {@link useEditEventCommentMutation}      PATCH  /events/{evtId}/comments/{cId}
 *   - {@link useDeleteEventCommentMutation}    DELETE /events/{evtId}/comments/{cId}
 *
 * The list query is suspense-ready and shares the
 * `['events', 'comments', wsId, calId, evtId]` cache row with all
 * write mutations so any add/edit/delete invalidates the list in a
 * single tick. The schema differs from task comments: event
 * `CommentResponse` exposes `userId` + `displayName` (not `authorId` /
 * `authorDisplayName`), so consumers must not assume the task-side
 * shape.
 *
 * Cache invalidation policy
 * -------------------------
 *   - Add / Edit / Delete → invalidate the comments list key only.
 *
 * The event detail DTO does not embed a comment count, so the detail
 * key is intentionally not refreshed here.
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

export type EventComment = components['schemas']['CommentResponse'];

export const eventCommentKeys = {
  list: (wsId: string, calId: string, evtId: string) =>
    ['events', 'comments', wsId, calId, evtId] as const,
};

/** GET /workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments. */
export function useEventCommentsQuery(
  wsId: string,
  calId: string,
  evtId: string,
): UseSuspenseQueryResult<EventComment[]> {
  return useSuspenseQuery({
    queryKey: eventCommentKeys.list(wsId, calId, evtId),
    queryFn: async (): Promise<EventComment[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments', {
            params: { path: { wsId, calId, evtId } },
          }),
        'Failed to load comments',
      );
      return data.comments ?? [];
    },
  });
}

export interface AddEventCommentArgs {
  wsId: string;
  calId: string;
  evtId: string;
  body: string;
}

/** POST /workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments. */
export function useAddEventCommentMutation(): UseMutationResult<
  EventComment,
  ApiError,
  AddEventCommentArgs
> {
  const qc = useQueryClient();
  return useMutation<EventComment, ApiError, AddEventCommentArgs>({
    mutationFn: async ({ wsId, calId, evtId, body }): Promise<EventComment> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments', {
            params: { path: { wsId, calId, evtId } },
            body: { body },
          }),
        'Failed to add comment',
      );
      return data;
    },
    onSuccess: (_data, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventCommentKeys.list(wsId, calId, evtId) });
    },
  });
}

export interface EditEventCommentArgs {
  wsId: string;
  calId: string;
  evtId: string;
  commentId: string;
  body: string;
}

/** PATCH /workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}. */
export function useEditEventCommentMutation(): UseMutationResult<
  void,
  ApiError,
  EditEventCommentArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, EditEventCommentArgs>({
    mutationFn: async ({ wsId, calId, evtId, commentId, body }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}', {
            params: { path: { wsId, calId, evtId, cId: commentId } },
            body: { body },
          }),
        'Failed to edit comment',
      );
    },
    onSuccess: (_void, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventCommentKeys.list(wsId, calId, evtId) });
    },
  });
}

export interface DeleteEventCommentArgs {
  wsId: string;
  calId: string;
  evtId: string;
  commentId: string;
}

/** DELETE /workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}. */
export function useDeleteEventCommentMutation(): UseMutationResult<
  void,
  ApiError,
  DeleteEventCommentArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteEventCommentArgs>({
    mutationFn: async ({ wsId, calId, evtId, commentId }): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}', {
            params: { path: { wsId, calId, evtId, cId: commentId } },
          }),
        'Failed to delete comment',
      );
    },
    onSuccess: (_void, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventCommentKeys.list(wsId, calId, evtId) });
    },
  });
}
