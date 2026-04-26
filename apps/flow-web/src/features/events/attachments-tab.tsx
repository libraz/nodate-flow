/**
 * AttachmentsTab — Attachments pane of the calendar event detail page.
 *
 * Lists attachment metadata associated with the event (filename, size,
 * uploader, created date) and exposes register-by-metadata + delete
 * actions. **The event-side API is metadata-only as of R6 W9** — there
 * is no presigned upload route and no presigned download route. The
 * task-side flow at `apps/flow-web/src/features/tasks/task-attachments.tsx`
 * uses `usePresignUpload` + `fetchDownloadUrl`; if those endpoints land
 * on the event side later, switch this file to mirror that flow.
 *
 * For now the "Add" affordance opens a small inline form where the
 * caller pastes the storage key, filename, content type, and byte size
 * they obtained out-of-band. This keeps the UI honest about what the
 * backend actually exposes rather than faking an upload pipeline.
 *
 * Hooks consumed:
 *   - {@link useEventAttachmentsQuery}            — suspense list read
 *   - {@link useAddEventAttachmentMutation}       — POST metadata
 *   - {@link useDeleteEventAttachmentMutation}    — DELETE with confirm
 *
 * Wrap in a `<Suspense>` boundary at the call site.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import {
  type EventAttachment,
  useAddEventAttachmentMutation,
  useDeleteEventAttachmentMutation,
  useEventAttachmentsQuery,
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
    } catch {
      toaster.show({ tone: 'danger', message: t('event.attachments.delete_error') });
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
          <span>{attachment.uploaderName}</span>
          {created ? <span>{created}</span> : null}
        </span>
      </div>
      <div className={styles.attachmentControls}>
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
  const addMutation = useAddEventAttachmentMutation();

  const [showForm, setShowForm] = useState(false);
  const [filename, setFilename] = useState('');
  const [storageKey, setStorageKey] = useState('');
  const [contentType, setContentType] = useState('');
  const [byteSizeRaw, setByteSizeRaw] = useState('');
  const formId = useId();
  const filenameId = `${formId}-filename`;
  const storageKeyId = `${formId}-storage-key`;
  const contentTypeId = `${formId}-content-type`;
  const byteSizeId = `${formId}-byte-size`;

  const resetForm = (): void => {
    setFilename('');
    setStorageKey('');
    setContentType('');
    setByteSizeRaw('');
  };

  const parsedByteSize = Number.parseInt(byteSizeRaw, 10);
  const validByteSize = !Number.isNaN(parsedByteSize) && parsedByteSize >= 0;
  const canSubmit =
    filename.trim().length > 0 &&
    storageKey.trim().length > 0 &&
    contentType.trim().length > 0 &&
    validByteSize &&
    !addMutation.isPending;

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!canSubmit) return;
    try {
      await addMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        input: {
          filename: filename.trim(),
          storageKey: storageKey.trim(),
          contentType: contentType.trim(),
          byteSize: parsedByteSize,
        },
      });
      toaster.show({ tone: 'success', message: t('event.attachments.upload_success') });
      resetForm();
      setShowForm(false);
    } catch {
      toaster.show({ tone: 'danger', message: t('event.attachments.add_error') });
    }
  };

  return (
    <div className={styles.tabPanel}>
      <div className={styles.tabHeader}>
        <span />
        <Button
          type="button"
          variant="default"
          size="sm"
          onClick={() => {
            setShowForm((v) => !v);
          }}
        >
          {t('event.attachments.add')}
        </Button>
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
      {showForm ? (
        <form
          className={styles.attachmentForm}
          onSubmit={(e) => {
            void handleSubmit(e);
          }}
        >
          <label
            htmlFor={filenameId}
            className={`${styles.attachmentFieldLabel} ${styles.attachmentFormFull}`}
          >
            <span>{t('event.attachments.field.filename')}</span>
            <Input
              id={filenameId}
              type="text"
              value={filename}
              onChange={(e) => {
                setFilename(e.target.value);
              }}
              required
            />
          </label>
          <label
            htmlFor={storageKeyId}
            className={`${styles.attachmentFieldLabel} ${styles.attachmentFormFull}`}
          >
            <span>{t('event.attachments.field.storage_key')}</span>
            <Input
              id={storageKeyId}
              type="text"
              value={storageKey}
              onChange={(e) => {
                setStorageKey(e.target.value);
              }}
              required
            />
          </label>
          <label htmlFor={contentTypeId} className={styles.attachmentFieldLabel}>
            <span>{t('event.attachments.field.content_type')}</span>
            <Input
              id={contentTypeId}
              type="text"
              value={contentType}
              onChange={(e) => {
                setContentType(e.target.value);
              }}
              required
            />
          </label>
          <label htmlFor={byteSizeId} className={styles.attachmentFieldLabel}>
            <span>{t('event.attachments.field.byte_size')}</span>
            <Input
              id={byteSizeId}
              type="number"
              min={0}
              value={byteSizeRaw}
              onChange={(e) => {
                setByteSizeRaw(e.target.value);
              }}
              required
            />
          </label>
          <div className={`${styles.addRowControls} ${styles.attachmentFormFull}`}>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                resetForm();
                setShowForm(false);
              }}
            >
              {t('event.comments.edit_cancel')}
            </Button>
            <Button type="submit" size="sm" disabled={!canSubmit}>
              {addMutation.isPending
                ? t('event.attachments.uploading')
                : t('event.attachments.upload')}
            </Button>
          </div>
        </form>
      ) : null}
    </div>
  );
}
