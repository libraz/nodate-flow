/**
 * Event attachments hooks — list, content-addressed upload, download,
 * and delete for the attachments associated with a single calendar
 * event.
 *
 *   - {@link useEventAttachmentsQuery}              GET    /events/{evtId}/attachments
 *   - {@link usePresignEventAttachmentMutation}     POST   /events/{evtId}/attachments/presign (+ optional PUT)
 *   - {@link fetchEventAttachmentDownloadUrl}       GET    /events/{evtId}/attachments/{attId}/download
 *   - {@link useEventAttachmentDownloadUrl}         GET    /events/{evtId}/attachments/{attId}/download (cache wrapper)
 *   - {@link useDeleteEventAttachmentMutation}      DELETE /events/{evtId}/attachments/{attId}
 *
 * **Content-addressed.** The presign endpoint takes the file's
 * SHA-256 (computed client-side via {@link sha256Hex}) and the server
 * runs dedup against {@code storage_objects}. When the digest already
 * exists the response carries {@code deduplicated: true} and no
 * {@code uploadUrl} — we skip the PUT entirely. Otherwise we stream
 * the file straight to the presigned PUT URL the API returned.
 *
 * All list hooks share the
 * {@code ['events', 'attachments', wsId, calId, evtId]} cache row so a
 * single invalidation refreshes the list view after every mutation.
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { ApiError, toApiError } from '../../lib/api-error';
import { sha256Hex } from '../../lib/crypto/sha256';
import { sdk } from '../../lib/sdk';

export type EventAttachment = components['schemas']['AttachmentResponse'];
export type PresignAttachmentBody = components['schemas']['PresignAttachmentInputBody'];
export type PresignAttachmentResult = components['schemas']['PresignAttachmentOutputBody'];

export const eventAttachmentKeys = {
  list: (wsId: string, calId: string, evtId: string) =>
    ['events', 'attachments', wsId, calId, evtId] as const,
  download: (wsId: string, calId: string, evtId: string, attId: string) =>
    ['events', 'attachments', wsId, calId, evtId, 'download', attId] as const,
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

export interface PresignEventAttachmentArgs {
  wsId: string;
  calId: string;
  evtId: string;
  file: File;
}

/**
 * Content-addressed upload mutation for event attachments.
 *
 * Mirrors the task-side `usePresignUpload`:
 *
 *   1. Hash the file body with SHA-256 client-side.
 *   2. POST {@code /events/{evtId}/attachments/presign} with the
 *      digest + metadata. The server creates the attachment row in the
 *      same transaction and either reuses an existing
 *      {@code storage_objects} entry (deduplicated=true) or returns a
 *      presigned PUT URL.
 *   3. If {@code deduplicated} is true, skip the upload. Otherwise PUT
 *      the file straight at the returned {@code uploadUrl}.
 *
 * Invalidates the event's attachment list on success regardless of
 * which branch ran.
 */
export function usePresignEventAttachmentMutation(): UseMutationResult<
  PresignAttachmentResult,
  ApiError,
  PresignEventAttachmentArgs
> {
  const qc = useQueryClient();
  return useMutation<PresignAttachmentResult, ApiError, PresignEventAttachmentArgs>({
    mutationFn: async ({ wsId, calId, evtId, file }): Promise<PresignAttachmentResult> => {
      const sha256 = await sha256Hex(file);
      const contentType = file.type || 'application/octet-stream';
      const { data, error } = await sdk.POST(
        '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/presign',
        {
          params: { path: { wsId, calId, evtId } },
          body: {
            filename: file.name,
            contentType,
            byteSize: file.size,
            sha256,
          },
        },
      );
      if (error || !data) throw toApiError(error, 'Failed to presign upload');

      // Dedup hit — the byte stream already exists in object storage;
      // the attachments row was created in the same transaction.
      if (data.deduplicated) return data;

      if (!data.uploadUrl) {
        throw new ApiError('UPLOAD_FAILED', 'Presign response missing uploadUrl');
      }

      const headers: Record<string, string> = {
        'Content-Type': contentType,
        ...(data.requiredHeaders ?? {}),
      };
      const res = await fetch(data.uploadUrl, {
        method: 'PUT',
        body: file,
        headers,
      });
      if (!res.ok) throw new ApiError('UPLOAD_FAILED', 'File upload failed');

      return data;
    },
    onSuccess: (_data, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventAttachmentKeys.list(wsId, calId, evtId) });
    },
  });
}

export interface FetchEventAttachmentDownloadUrlArgs {
  wsId: string;
  calId: string;
  evtId: string;
  attachmentId: string;
}

/**
 * One-shot fetch of a presigned GET URL for an event attachment. Use
 * this from a click handler ({@code window.open(url, '_blank')}) when
 * you only need the URL once and do not want to keep it cached.
 */
export async function fetchEventAttachmentDownloadUrl({
  wsId,
  calId,
  evtId,
  attachmentId,
}: FetchEventAttachmentDownloadUrlArgs): Promise<string> {
  const { data, error } = await sdk.GET(
    '/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}/download',
    { params: { path: { wsId, calId, evtId, attId: attachmentId } } },
  );
  if (error || !data) throw toApiError(error, 'Failed to get download URL');
  return data.downloadUrl;
}

/**
 * Cached variant of {@link fetchEventAttachmentDownloadUrl}.
 *
 * Useful when a component wants to render a real {@code <a href>} or
 * to prefetch the URL eagerly. Presigned GET URLs are short-lived;
 * the consumer should treat the returned URL as a one-shot value and
 * not rely on it surviving across long-lived sessions. The query is
 * keyed by the attachment id so each row gets its own cache slot.
 */
export function useEventAttachmentDownloadUrl(
  wsId: string,
  calId: string,
  evtId: string,
  attachmentId: string,
): UseSuspenseQueryResult<string> {
  return useSuspenseQuery({
    queryKey: eventAttachmentKeys.download(wsId, calId, evtId, attachmentId),
    queryFn: () => fetchEventAttachmentDownloadUrl({ wsId, calId, evtId, attachmentId }),
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
