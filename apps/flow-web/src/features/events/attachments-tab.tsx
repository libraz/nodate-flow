/**
 * AttachmentsTab — Attachments pane of the calendar event detail page.
 *
 * Lists attachment metadata associated with the event (filename, size,
 * uploader, created date) and exposes content-addressed upload,
 * download, and delete actions. The upload pipeline mirrors the
 * task-side flow at
 * `apps/flow-web/src/features/tasks/task-attachments.tsx`:
 *
 *   1. The user picks a file via the hidden `<input type="file">`.
 *   2. We compute its SHA-256 client-side and POST the digest +
 *      metadata to `/events/{evtId}/attachments/presign`.
 *   3. If the response carries `deduplicated: true`, we skip the PUT
 *      entirely (the byte stream already exists in storage).
 *      Otherwise we PUT the file to the signed upload URL served by the API.
 *
 * Hooks consumed:
 *   - {@link useEventAttachmentsQuery}                — suspense list read
 *   - {@link usePresignEventAttachmentMutation}       — content-addressed upload
 *   - {@link fetchEventAttachmentDownloadUrl}         — one-shot download URL
 *   - {@link useDeleteEventAttachmentMutation}        — DELETE with confirm
 *
 * Wrap in a `<Suspense>` boundary at the call site.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useQueries, useQueryClient } from '@tanstack/react-query';
import { type ChangeEvent, type ReactElement, Suspense, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  AttachmentFileSlot,
  AttachmentThumbnail,
  GALLERY_REFRESH_MS,
  LazyImageGallery,
  MAX_AUTO_PREVIEW_BYTES,
} from '../../components/image-gallery';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import { formatBytes } from '../../lib/format-bytes';
import {
  type EventAttachment,
  eventAttachmentPreviewQueryOptions,
  fetchEventAttachmentDownloadUrl,
  useDeleteEventAttachmentMutation,
  useEventAttachmentPreview,
  useEventAttachmentsQuery,
  usePresignEventAttachmentMutation,
} from './attachments-api';
import styles from './event-detail-page.module.css';

export interface AttachmentsTabProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

/** Remaining lifetime under which a failed thumbnail load is retried with a fresh URL. */
const PREVIEW_RETRY_WINDOW_MS = 60_000;

interface AttachmentScope {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

function RowThumbnail({
  scope,
  attachment,
  onOpen,
}: {
  scope: AttachmentScope;
  attachment: EventAttachment;
  onOpen: (attachmentId: string, button: HTMLButtonElement) => void;
}): ReactElement {
  const oversized = attachment.byteSize > MAX_AUTO_PREVIEW_BYTES;
  const preview = useEventAttachmentPreview(
    {
      wsId: scope.workspaceId,
      calId: scope.calendarId,
      evtId: scope.eventId,
      attachmentId: attachment.id,
    },
    attachment.previewable && !oversized,
  );
  const [failed, setFailed] = useState(false);
  const [retried, setRetried] = useState(false);

  if (!attachment.previewable) return <AttachmentFileSlot />;

  const handleImageError = async (): Promise<void> => {
    const current = preview.data;
    const nearExpiry =
      current !== undefined && current.expiresAt * 1000 - Date.now() < PREVIEW_RETRY_WINDOW_MS;
    if (retried || !nearExpiry) {
      setFailed(true);
      return;
    }
    setRetried(true);
    const result = await preview.refetch();
    // The same URL within one expiry window cannot load any better.
    if (result.data === undefined || result.data.previewUrl === current.previewUrl) {
      setFailed(true);
    }
  };

  let status: 'loading' | 'ready' | 'unavailable' | 'oversized' = 'ready';
  if (oversized) status = 'oversized';
  else if (failed || preview.isError) status = 'unavailable';
  else if (preview.data === undefined) status = 'loading';

  return (
    <AttachmentThumbnail
      filename={attachment.filename}
      status={status}
      {...(preview.data ? { src: preview.data.previewUrl } : {})}
      onImageError={() => {
        void handleImageError();
      }}
      onOpen={(button) => onOpen(attachment.id, button)}
    />
  );
}

interface AttachmentRowProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  attachment: EventAttachment;
  locale: string;
  onOpenPreview: (attachmentId: string, button: HTMLButtonElement) => void;
}

function AttachmentRow({
  workspaceId,
  calendarId,
  eventId,
  attachment,
  locale,
  onOpenPreview,
}: AttachmentRowProps): ReactElement {
  const { t } = useTranslation('calendar-events');
  const { t: tCommon } = useTranslation('common');
  const deleteMutation = useDeleteEventAttachmentMutation();

  const handleDelete = async (): Promise<void> => {
    const ok = await confirmAction({
      message: t('event.attachments.delete_confirm'),
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await deleteMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        attachmentId: attachment.id,
      });
      toaster.show({ tone: 'success', message: t('event.attachments.delete_success') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.attachments.delete_error'),
      });
    }
  };

  const handleDownload = async (): Promise<void> => {
    try {
      const url = await fetchEventAttachmentDownloadUrl({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        attachmentId: attachment.id,
      });
      window.open(url, '_blank');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.attachments.download_error'),
      });
    }
  };

  const created = formatEpochDateTime(attachment.createdAt, locale) ?? '';

  return (
    <div className={styles.attachmentRow}>
      <RowThumbnail
        scope={{ workspaceId, calendarId, eventId }}
        attachment={attachment}
        onOpen={onOpenPreview}
      />
      <div className={styles.attachmentIdentity}>
        <span className={styles.attachmentName}>{attachment.filename}</span>
        <span className={styles.attachmentMeta}>
          <span>{formatBytes(attachment.byteSize, locale, tCommon)}</span>
          <span>{attachment.contentType}</span>
          <span>{attachment.uploaderName || t('common:common.deactivated_user')}</span>
          {created ? <span>{created}</span> : null}
        </span>
      </div>
      <div className={styles.attachmentControls}>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          aria-label={t('event.attachments.download')}
          onClick={() => {
            void handleDownload();
          }}
        >
          {t('event.attachments.download')}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={deleteMutation.isPending}
          onClick={() => {
            void handleDelete();
          }}
        >
          {t('event.attachments.delete')}
        </Button>
      </div>
    </div>
  );
}

interface GalleryImage {
  id: string;
  filename: string;
}

function EventGallery({
  scope,
  images,
  index,
  onClose,
  onDownload,
  onExited,
}: {
  scope: AttachmentScope;
  images: GalleryImage[];
  index: number;
  onClose: () => void;
  onDownload: (id: string) => void;
  onExited: () => void;
}): ReactElement {
  const results = useQueries({
    queries: images.map((image) => ({
      ...eventAttachmentPreviewQueryOptions({
        wsId: scope.workspaceId,
        calId: scope.calendarId,
        evtId: scope.eventId,
        attachmentId: image.id,
      }),
      refetchInterval: GALLERY_REFRESH_MS,
    })),
  });
  const slides = images.flatMap((image, i) => {
    const src = results[i]?.data?.previewUrl;
    return src ? [{ id: image.id, src, filename: image.filename }] : [];
  });
  return (
    <Suspense fallback={null}>
      <LazyImageGallery
        slides={slides}
        index={index}
        onClose={onClose}
        onDownload={onDownload}
        onExited={onExited}
      />
    </Suspense>
  );
}

/**
 * AttachmentsTab — see file-level docstring.
 */
export default function AttachmentsTab({
  workspaceId,
  calendarId,
  eventId,
}: AttachmentsTabProps): ReactElement {
  const { t, i18n } = useTranslation('calendar-events');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data: attachments } = useEventAttachmentsQuery(workspaceId, calendarId, eventId);
  const presignUpload = usePresignEventAttachmentMutation();
  const { entries, onExited } = usePresenceList(attachments, (a) => a.id);
  const staggerFor = useFirstLoadStagger();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const queryClient = useQueryClient();
  const [gallery, setGallery] = useState<{ images: GalleryImage[]; index: number } | null>(null);
  const openerRef = useRef<HTMLButtonElement | null>(null);
  const scope: AttachmentScope = { workspaceId, calendarId, eventId };

  const handleOpenPreview = async (
    attachmentId: string,
    button: HTMLButtonElement,
  ): Promise<void> => {
    const candidates = attachments.filter((a) => a.previewable);
    const settled = await Promise.allSettled(
      candidates.map((a) =>
        queryClient.fetchQuery(
          eventAttachmentPreviewQueryOptions({
            wsId: workspaceId,
            calId: calendarId,
            evtId: eventId,
            attachmentId: a.id,
          }),
        ),
      ),
    );
    const images = candidates
      .filter((_, i) => settled[i]?.status === 'fulfilled')
      .map((a) => ({ id: a.id, filename: a.filename }));
    const index = images.findIndex((image) => image.id === attachmentId);
    if (index < 0) {
      toaster.show({ tone: 'danger', message: t('common:gallery.load_failed') });
      return;
    }
    openerRef.current = button;
    setGallery({ images, index });
  };

  const handleGalleryDownload = async (attachmentId: string): Promise<void> => {
    try {
      const url = await fetchEventAttachmentDownloadUrl({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        attachmentId,
      });
      window.open(url, '_blank');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.attachments.download_error'),
      });
    }
  };

  const handleFileChange = async (e: ChangeEvent<HTMLInputElement>): Promise<void> => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      const result = await presignUpload.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        file,
      });
      toaster.show({
        tone: 'success',
        message: result.deduplicated
          ? t('event.attachments.upload_dedup_success')
          : t('event.attachments.upload_success'),
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.attachments.upload_error'),
      });
    }
    // Reset the input so the same file can be re-selected.
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  return (
    <div className={styles.tabPanel}>
      <div className={styles.tabHeader}>
        <span />
        <div>
          <input
            ref={fileInputRef}
            type="file"
            hidden
            onChange={(e) => {
              void handleFileChange(e);
            }}
          />
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={presignUpload.isPending}
            aria-busy={presignUpload.isPending || undefined}
            onClick={() => fileInputRef.current?.click()}
          >
            {t('event.attachments.upload')}
          </Button>
        </div>
      </div>
      {entries.length === 0 ? (
        <p className={styles.empty}>{t('event.attachments.empty')}</p>
      ) : (
        <ul className={styles.itemList}>
          {entries.map(({ key, item, present }, index) => (
            <PresenceItem
              key={key}
              as="li"
              present={present}
              onExited={() => onExited(key)}
              className={motion.enter}
              style={staggerFor(index)}
            >
              <AttachmentRow
                workspaceId={workspaceId}
                calendarId={calendarId}
                eventId={eventId}
                attachment={item}
                locale={locale}
                onOpenPreview={(id, button) => {
                  void handleOpenPreview(id, button);
                }}
              />
            </PresenceItem>
          ))}
        </ul>
      )}
      {gallery ? (
        <EventGallery
          scope={scope}
          images={gallery.images}
          index={gallery.index}
          onClose={() => setGallery(null)}
          onDownload={(id) => {
            void handleGalleryDownload(id);
          }}
          onExited={() => openerRef.current?.focus()}
        />
      ) : null}
    </div>
  );
}
