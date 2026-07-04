/**
 * TaskAttachments — file upload (presigned PUT), download, and delete for task attachments.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
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
    <li
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.75rem',
        paddingBlock: '0.375rem',
      }}
    >
      <div style={{ flex: 1, minInlineSize: 0, display: 'flex', flexDirection: 'column' }}>
        <span
          style={{
            fontWeight: 500,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {attachment.filename}
        </span>
        <span
          style={{
            fontSize: '0.8125rem',
            color: 'var(--nf-color-fg-muted)',
            display: 'flex',
            gap: '0.5rem',
            flexWrap: 'wrap',
          }}
        >
          <span>{formatBytes(attachment.byteSize, locale, t)}</span>
          <span>{attachment.contentType}</span>
          <span>
            {t('tasks.attachments.uploaded_by', { name: attachment.uploaderDisplayName })}
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
    </li>
  );
}

/** Attachment list panel for the task detail view with file upload. */
export default function TaskAttachments({ taskId }: TaskAttachmentsProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: attachments } = useListAttachments(taskId);
  const locale = i18n.resolvedLanguage ?? 'en';
  const presignUpload = usePresignUpload();
  const fileInputRef = useRef<HTMLInputElement>(null);

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
    <section style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-lg)' }}>{t('tasks.attachments.title')}</h2>
        <div>
          <input
            ref={fileInputRef}
            type="file"
            style={{ display: 'none' }}
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
      {attachments.length === 0 ? (
        <p style={{ color: 'var(--nf-color-fg-muted)', margin: 0 }}>
          {t('tasks.attachments.empty')}
        </p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.25rem',
          }}
        >
          {attachments.map((a) => (
            <AttachmentRow key={a.id} attachment={a} taskId={taskId} locale={locale} />
          ))}
        </ul>
      )}
    </section>
  );
}
