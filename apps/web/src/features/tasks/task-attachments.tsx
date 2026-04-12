/**
 * TaskAttachments — lists attachment metadata for a task with delete support.
 *
 * Renders a compact list of files attached to the task. Each row shows the
 * filename, human-readable size, content type, and uploader name, plus a
 * delete button guarded by a confirmation dialog.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { toaster } from '@nodate-flow/ui/primitives/toast';
import { confirmAction } from '../../lib/confirm-action';
import { type TaskAttachment, useDeleteAttachment, useListAttachments } from './api';

interface TaskAttachmentsProps {
  taskId: string;
}

/** Format byte count into a human-readable string using the user's locale. */
function formatBytes(bytes: number, locale: string): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'] as const;
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const formatted = new Intl.NumberFormat(locale, {
    maximumFractionDigits: unitIndex === 0 ? 0 : 1,
  }).format(value);
  return `${formatted} ${units[unitIndex]}`;
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
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.errors.update_failed') });
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
            color: 'var(--color-muted)',
            display: 'flex',
            gap: '0.5rem',
            flexWrap: 'wrap',
          }}
        >
          <span>{formatBytes(attachment.byteSize, locale)}</span>
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

/** Attachment list panel for the task detail view. */
export default function TaskAttachments({ taskId }: TaskAttachmentsProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: attachments } = useListAttachments(taskId);
  const locale = i18n.resolvedLanguage ?? 'en';

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      <h2 style={{ margin: 0, fontSize: '1.125rem' }}>{t('tasks.attachments.title')}</h2>
      {attachments.length === 0 ? (
        <p style={{ color: 'var(--color-muted)', margin: 0 }}>{t('tasks.attachments.empty')}</p>
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
