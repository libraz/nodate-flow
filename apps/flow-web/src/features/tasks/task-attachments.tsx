/**
 * TaskAttachments — file upload (API-signed URL), download, and delete for task attachments.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
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
import css from './task-attachments.module.css';

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

  return (
    <section className={css.section}>
      <div className={css.header}>
        <h2 className={css.title}>{t('tasks.attachments.title')}</h2>
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
            onClick={() => fileInputRef.current?.click()}
            disabled={presignUpload.isPending}
          >
            {presignUpload.isPending
              ? t('tasks.attachments.uploading')
              : t('tasks.attachments.upload')}
          </Button>
        </div>
      </div>
      {entries.length === 0 ? (
        <p className={css.empty}>{t('tasks.attachments.empty')}</p>
      ) : (
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
      )}
    </section>
  );
}
