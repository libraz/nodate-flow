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
 *      entirely (the byte stream already exists in object storage).
 *      Otherwise we stream the file straight to the presigned PUT URL.
 *
 * Hooks consumed:
 *   - {@link useEventAttachmentsQuery}                — suspense list read
 *   - {@link usePresignEventAttachmentMutation}       — content-addressed upload
 *   - {@link fetchEventAttachmentDownloadUrl}         — one-shot download URL
 *   - {@link useDeleteEventAttachmentMutation}        — DELETE with confirm
 *
 * Wrap in a `<Suspense>` boundary at the call site.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ChangeEvent, type ReactElement, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import {
  type EventAttachment,
  fetchEventAttachmentDownloadUrl,
  useDeleteEventAttachmentMutation,
  useEventAttachmentsQuery,
  usePresignEventAttachmentMutation,
} from './attachments-api';
import styles from './event-detail-page.module.css';

export interface AttachmentsTabProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

/**
 * Format a byte count using the user's locale and a binary scale,
 * resolving the unit suffix through static i18n keys (no dynamic
 * template lookups, per the web feature conventions).
 */
function formatBytes(bytes: number, locale: string, t: (key: string) => string): string {
  const unitLabels: readonly string[] = [
    t('event.attachments.size_unit.b'),
    t('event.attachments.size_unit.kb'),
    t('event.attachments.size_unit.mb'),
    t('event.attachments.size_unit.gb'),
  ];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < unitLabels.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const formatted = new Intl.NumberFormat(locale, {
    maximumFractionDigits: unitIndex === 0 ? 0 : 1,
  }).format(value);
  const label = unitLabels[unitIndex] ?? unitLabels[0];
  return `${formatted} ${label}`;
}

interface AttachmentRowProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  attachment: EventAttachment;
  locale: string;
}

function AttachmentRow({
  workspaceId,
  calendarId,
  eventId,
  attachment,
  locale,
}: AttachmentRowProps): ReactElement {
  const { t } = useTranslation('calendar-events');
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
    <li className={styles.attachmentRow}>
      <div className={styles.attachmentIdentity}>
        <span className={styles.attachmentName}>{attachment.filename}</span>
        <span className={styles.attachmentMeta}>
          <span>{formatBytes(attachment.byteSize, locale, t)}</span>
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
    </li>
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
  const fileInputRef = useRef<HTMLInputElement>(null);

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
            style={{ display: 'none' }}
            onChange={(e) => {
              void handleFileChange(e);
            }}
          />
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={presignUpload.isPending}
            onClick={() => fileInputRef.current?.click()}
          >
            {presignUpload.isPending
              ? t('event.attachments.uploading')
              : t('event.attachments.upload')}
          </Button>
        </div>
      </div>
      {attachments.length === 0 ? (
        <p className={styles.empty}>{t('event.attachments.empty')}</p>
      ) : (
        <ul className={styles.itemList}>
          {attachments.map((attachment) => (
            <AttachmentRow
              key={attachment.id}
              workspaceId={workspaceId}
              calendarId={calendarId}
              eventId={eventId}
              attachment={attachment}
              locale={locale}
            />
          ))}
        </ul>
      )}
    </div>
  );
}
