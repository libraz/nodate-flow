/**
 * TaskAttachments — file upload (API-signed URL), download, and delete for task attachments.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { Paperclip, Upload } from 'lucide-react';
import { type ChangeEvent, type ReactElement, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatBytes } from '../../lib/format-bytes';
import {
  fetchDownloadUrl,
  type TaskAttachment,
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

function AttachmentRow({
  attachment,
  taskId,
  locale,
}: {
  attachment: TaskAttachment;
  taskId: string;
  locale: string;
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

/** Attachment list panel for the task detail view with file upload. */
export default function TaskAttachments({ taskId }: TaskAttachmentsProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: attachments } = useListAttachments(taskId);
  const locale = i18n.resolvedLanguage ?? 'en';
  const presignUpload = usePresignUpload();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { entries, onExited } = usePresenceList(attachments, (a) => a.id);

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
            <AttachmentRow attachment={a} taskId={taskId} locale={locale} />
          </PresenceItem>
        ))}
      </ul>
    </section>
  );
}
