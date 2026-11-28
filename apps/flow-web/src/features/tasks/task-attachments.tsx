/**
 * TaskAttachments — file upload (API-signed URL), download, and delete for task attachments.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useQueries, useQueryClient } from '@tanstack/react-query';
import { Paperclip, Upload } from 'lucide-react';
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
import { formatBytes } from '../../lib/format-bytes';
import {
  attachmentPreviewQueryOptions,
  fetchDownloadUrl,
  type TaskAttachment,
  useAttachmentPreview,
  useDeleteAttachment,
  useListAttachments,
  usePresignUpload,
} from './api';
import TaskActionButton from './task-action-button';
import css from './task-attachments.module.css';
import TaskSectionHeading from './task-section-heading';

interface TaskAttachmentsProps {
  taskId: string;
}

/** Remaining lifetime under which a failed thumbnail load is retried with a fresh URL. */
const PREVIEW_RETRY_WINDOW_MS = 60_000;

function RowThumbnail({
  attachment,
  taskId,
  onOpen,
}: {
  attachment: TaskAttachment;
  taskId: string;
  onOpen: (attachmentId: string, button: HTMLButtonElement) => void;
}): ReactElement {
  const oversized = attachment.byteSize > MAX_AUTO_PREVIEW_BYTES;
  const preview = useAttachmentPreview(taskId, attachment.id, attachment.previewable && !oversized);
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

function AttachmentRow({
  attachment,
  taskId,
  locale,
  onOpenPreview,
}: {
  attachment: TaskAttachment;
  taskId: string;
  locale: string;
  onOpenPreview: (attachmentId: string, button: HTMLButtonElement) => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const deleteAttachment = useDeleteAttachment();

  const handleDelete = async (): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('tasks.attachments.delete_confirm'),
      tone: 'danger',
    });
    if (!confirmed) return;
    try {
      await deleteAttachment.mutateAsync({ taskId, attachmentId: attachment.id });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  const handleDownload = async (): Promise<void> => {
    try {
      const url = await fetchDownloadUrl(taskId, attachment.id);
      window.open(url, '_blank');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  return (
    <div className={css.row}>
      <RowThumbnail attachment={attachment} taskId={taskId} onOpen={onOpenPreview} />
      <div className={css.rowBody}>
        <span title={attachment.filename} className={css.filename}>
          {attachment.filename}
        </span>
        <span className={css.meta}>
          <span>{formatBytes(attachment.byteSize, locale, t)}</span>
          <span>{attachment.contentType}</span>
          <span>
            {t('tasks.attachments.uploaded_by', {
              name: attachment.uploaderDisplayName || t('common.deactivated_user'),
            })}
          </span>
        </span>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label={t('tasks.attachments.download')}
        onClick={() => {
          void handleDownload();
        }}
      >
        {t('tasks.attachments.download')}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label={t('tasks.attachments.delete')}
        onClick={() => {
          void handleDelete();
        }}
        disabled={deleteAttachment.isPending}
      >
        {deleteAttachment.isPending
          ? t('tasks.attachments.deleting')
          : t('tasks.attachments.delete')}
      </Button>
    </div>
  );
}

interface GalleryImage {
  id: string;
  filename: string;
}

function TaskGallery({
  taskId,
  images,
  index,
  onClose,
  onDownload,
  onExited,
}: {
  taskId: string;
  images: GalleryImage[];
  index: number;
  onClose: () => void;
  onDownload: (id: string) => void;
  onExited: () => void;
}): ReactElement {
  const results = useQueries({
    queries: images.map((image) => ({
      ...attachmentPreviewQueryOptions(taskId, image.id),
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

/** Attachment list panel for the task detail view with file upload. */
export default function TaskAttachments({ taskId }: TaskAttachmentsProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: attachments } = useListAttachments(taskId);
  const locale = i18n.resolvedLanguage ?? 'en';
  const presignUpload = usePresignUpload();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { entries, onExited } = usePresenceList(attachments, (a) => a.id);
  const queryClient = useQueryClient();
  const [gallery, setGallery] = useState<{ images: GalleryImage[]; index: number } | null>(null);
  const openerRef = useRef<HTMLButtonElement | null>(null);

  const handleOpenPreview = async (
    attachmentId: string,
    button: HTMLButtonElement,
  ): Promise<void> => {
    const candidates = attachments.filter((a) => a.previewable);
    const settled = await Promise.allSettled(
      candidates.map((a) => queryClient.fetchQuery(attachmentPreviewQueryOptions(taskId, a.id))),
    );
    const images = candidates
      .filter((_, i) => settled[i]?.status === 'fulfilled')
      .map((a) => ({ id: a.id, filename: a.filename }));
    const index = images.findIndex((image) => image.id === attachmentId);
    if (index < 0) {
      toaster.show({ tone: 'danger', message: t('gallery.load_failed') });
      return;
    }
    openerRef.current = button;
    setGallery({ images, index });
  };

  const handleGalleryDownload = async (attachmentId: string): Promise<void> => {
    try {
      const url = await fetchDownloadUrl(taskId, attachmentId);
      window.open(url, '_blank');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.errors.update_failed'),
      });
    }
  };

  const handleFileChange = async (e: ChangeEvent<HTMLInputElement>): Promise<void> => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      await presignUpload.mutateAsync({ taskId, file });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.attachments.upload_failed'),
      });
    }
    // Reset the file input so the same file can be re-selected.
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  const fileInput = (
    <input
      ref={fileInputRef}
      type="file"
      hidden
      onChange={(e) => {
        void handleFileChange(e);
      }}
    />
  );
  const uploadLabel = presignUpload.isPending
    ? t('tasks.attachments.uploading')
    : t('tasks.attachments.upload');

  // No files yet: one add button the detail page gathers into its "Add to this task" row.
  if (entries.length === 0) {
    return (
      <div data-compact="">
        {fileInput}
        <TaskActionButton
          icon={<Paperclip size={14} aria-hidden />}
          aria-label={uploadLabel}
          onClick={() => fileInputRef.current?.click()}
          disabled={presignUpload.isPending}
        >
          {presignUpload.isPending
            ? t('tasks.attachments.uploading')
            : t('tasks.detail.add_to_task.attachment')}
        </TaskActionButton>
      </div>
    );
  }

  return (
    <section className={css.section}>
      <TaskSectionHeading
        title={t('tasks.attachments.title')}
        trailing={
          <div>
            {fileInput}
            <TaskActionButton
              icon={<Upload size={14} aria-hidden />}
              onClick={() => fileInputRef.current?.click()}
              disabled={presignUpload.isPending}
            >
              {uploadLabel}
            </TaskActionButton>
          </div>
        }
      />
      <ul className={css.list}>
        {entries.map(({ key, item: a, present }) => (
          <PresenceItem
            key={key}
            as="li"
            present={present}
            onExited={() => onExited(key)}
            className={motion.enter}
          >
            <AttachmentRow
              attachment={a}
              taskId={taskId}
              locale={locale}
              onOpenPreview={(id, button) => {
                void handleOpenPreview(id, button);
              }}
            />
          </PresenceItem>
        ))}
      </ul>
      {gallery ? (
        <TaskGallery
          taskId={taskId}
          images={gallery.images}
          index={gallery.index}
          onClose={() => setGallery(null)}
          onDownload={(id) => {
            void handleGalleryDownload(id);
          }}
          onExited={() => openerRef.current?.focus()}
        />
      ) : null}
    </section>
  );
}
