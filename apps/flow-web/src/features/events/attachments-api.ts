/**
 * Event attachments hooks — list, register metadata, and delete the
 * attachments associated with a single calendar event.
 *
 *   - {@link useEventAttachmentsQuery}            GET    /events/{evtId}/attachments
 *   - {@link useAddEventAttachmentMutation}       POST   /events/{evtId}/attachments
 *   - {@link useDeleteEventAttachmentMutation}    DELETE /events/{evtId}/attachments/{attId}
 *
 * **Metadata-only.** Unlike the task-side flow
 * (`/tasks/{id}/attachments/presign` + `POST` + presigned PUT), the
 * calendar-event endpoints do **not** expose a presign route or a
 * download-URL route today. The attach payload requires
 * `filename`, `contentType`, `byteSize`, and a `storageKey` the
 * caller already owns (e.g. an external bucket key, a paste URL, or
 * a value from a separate upload pipeline). If a presign endpoint is
 * added later, this module should grow `useEventAttachmentPresignMutation`
 * + `fetchEventAttachmentDownloadUrl` to mirror the task-side helpers
 * in `apps/flow-web/src/features/tasks/api.ts`.
 *
 * All hooks share the `['events', 'attachments', wsId, calId, evtId]`
 * cache row.
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

export type EventAttachment = components['schemas']['AttachmentResponse'];
export type CreateAttachmentBody = components['schemas']['CreateAttachmentInputBody'];

export const eventAttachmentKeys = {
  list: (wsId: string, calId: string, evtId: string) =>
    ['events', 'attachments', wsId, calId, evtId] as const,
};

/** GET /workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments. */
export function useEventAttachmentsQuery(
  wsId: string,
  calId: string,
  evtId: string,
): UseSuspenseQueryResult<EventAttachment[]> {
  return useSuspenseQuery({
    queryKey: eventAttachmentKeys.list(wsId, calId, evtId),
    queryFn: async (): Promise<EventAttachment[]> => {
      const { data, error } = await sdk.GET(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments',
        { params: { path: { wsId, calId, evtId } } },
      );
      if (error || !data) throw toApiError(error, 'Failed to load attachments');
      return data.attachments ?? [];
    },
  });
}

export interface AddEventAttachmentArgs {
  wsId: string;
  calId: string;
  evtId: string;
  input: CreateAttachmentBody;
}

/** POST /workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments. */
export function useAddEventAttachmentMutation(): UseMutationResult<
  EventAttachment,
  ApiError,
  AddEventAttachmentArgs
> {
  const qc = useQueryClient();
  return useMutation<EventAttachment, ApiError, AddEventAttachmentArgs>({
    mutationFn: async ({ wsId, calId, evtId, input }): Promise<EventAttachment> => {
      const { data, error } = await sdk.POST(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments',
        { params: { path: { wsId, calId, evtId } }, body: input },
      );
      if (error || !data) throw toApiError(error, 'Failed to add attachment');
      return data;
    },
    onSuccess: (_data, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventAttachmentKeys.list(wsId, calId, evtId) });
    },
  });
}

export interface DeleteEventAttachmentArgs {
  wsId: string;
  calId: string;
  evtId: string;
  attachmentId: string;
}

/** DELETE /workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}. */
export function useDeleteEventAttachmentMutation(): UseMutationResult<
  void,
  ApiError,
  DeleteEventAttachmentArgs
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteEventAttachmentArgs>({
    mutationFn: async ({ wsId, calId, evtId, attachmentId }): Promise<void> => {
      const { error } = await sdk.DELETE(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}',
        { params: { path: { wsId, calId, evtId, attId: attachmentId } } },
      );
      if (error) throw toApiError(error, 'Failed to delete attachment');
    },
    onSuccess: (_void, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventAttachmentKeys.list(wsId, calId, evtId) });
    },
  });
}
