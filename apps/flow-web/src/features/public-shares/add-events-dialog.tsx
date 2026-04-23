/**
 * AddEventsDialog — picker that lists workspace calendar events in a
 * date range and lets the admin attach a selection to the share.
 *
 * Confidential events are excluded from the list (and server re-rejects at
 * attach time as a safety gate). Already-attached events are shown but not
 * selectable.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, Suspense, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDate } from '../../lib/format';

import {
  type CrossCalendarEvent,
  useAttachEventsToShare,
  useWorkspaceCalendarEventsQuery,
} from './api';

export interface AddEventsDialogProps {
  workspaceId: string;
  shareId: string;
  open: boolean;
  attachedIds: Set<string>;
  onClose: () => void;
}

/** Compute a default [today, +90 days] window as YYYY-MM-DD strings. */
function defaultRange(): { from: string; to: string } {
  const now = new Date();
  const to = new Date(now);
  to.setDate(to.getDate() + 90);
  return { from: ymd(now), to: ymd(to) };
}

function ymd(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/** Convert a YYYY-MM-DD string to an ISO datetime at local midnight. */
function startOfDayIso(date: string): string {
  return new Date(`${date}T00:00:00`).toISOString();
}

function endOfDayIso(date: string): string {
  return new Date(`${date}T23:59:59.999`).toISOString();
}

export default function AddEventsDialog({
  workspaceId,
  shareId,
  open,
  attachedIds,
  onClose,
}: AddEventsDialogProps): ReactElement {
  const { t } = useTranslation('settings');
  const { t: tCommon, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = tCommon('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });
  const defaults = useMemo(defaultRange, []);
  const [from, setFrom] = useState(defaults.from);
  const [to, setTo] = useState(defaults.to);

  return (
    <Dialog open={open} onClose={onClose} title={t('workspace.public_shares.detail.picker.title')}>
      <div
        style={{ display: 'flex', flexDirection: 'column', gap: '1rem', minInlineSize: '28rem' }}
      >
        <div style={{ display: 'flex', gap: '0.75rem' }}>
          <FormField
            label={t('workspace.public_shares.detail.picker.range_from')}
            style={{ flex: 1 }}
          >
            {() => (
              <DatePicker
                value={from}
                onChange={setFrom}
                weekdayLabels={weekdayLabels}
                formatMonthYear={formatMonthYear}
                prevLabel={tCommon('calendar.prev')}
                nextLabel={tCommon('calendar.next')}
                triggerLabel={from ? formatDate(from, locale) : tCommon('common.date.placeholder')}
              />
            )}
          </FormField>
          <FormField
            label={t('workspace.public_shares.detail.picker.range_to')}
            style={{ flex: 1 }}
          >
            {() => (
              <DatePicker
                value={to}
                onChange={setTo}
                weekdayLabels={weekdayLabels}
                formatMonthYear={formatMonthYear}
                prevLabel={tCommon('calendar.prev')}
                nextLabel={tCommon('calendar.next')}
                triggerLabel={to ? formatDate(to, locale) : tCommon('common.date.placeholder')}
                {...(from ? { minDate: from } : {})}
              />
            )}
          </FormField>
        </div>

        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.8125rem' }}>
          {t('workspace.public_shares.detail.picker.hint')}
        </p>

        <Suspense
          fallback={
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
              <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
              <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
            </div>
          }
        >
          <PickerBody
            workspaceId={workspaceId}
            shareId={shareId}
            startIso={startOfDayIso(from)}
            endIso={endOfDayIso(to)}
            attachedIds={attachedIds}
            onClose={onClose}
          />
        </Suspense>
      </div>
    </Dialog>
  );
}

interface PickerBodyProps {
  workspaceId: string;
  shareId: string;
  startIso: string;
  endIso: string;
  attachedIds: Set<string>;
  onClose: () => void;
}

function PickerBody({
  workspaceId,
  shareId,
  startIso,
  endIso,
  attachedIds,
  onClose,
}: PickerBodyProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data } = useWorkspaceCalendarEventsQuery(workspaceId, startIso, endIso);
  const attach = useAttachEventsToShare(workspaceId, shareId);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const visible = useMemo(() => data.filter((ev) => ev.visibility !== 'confidential'), [data]);

  const toggle = (eventId: string): void => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(eventId)) next.delete(eventId);
      else next.add(eventId);
      return next;
    });
  };

  const handleAttach = async (): Promise<void> => {
    if (selected.size === 0) return;
    try {
      const result = await attach.mutateAsync(Array.from(selected));
      toaster.show({
        tone: 'success',
        message: t('workspace.public_shares.detail.picker.attached_toast', {
          attached: result.attached,
          skipped: result.skipped,
        }),
      });
      setSelected(new Set());
      onClose();
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.public_shares.detail.errors.attach_failed'),
      });
    }
  };

  if (visible.length === 0) {
    return (
      <>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('workspace.public_shares.detail.picker.no_candidates')}
        </p>
        <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t('workspace.public_shares.detail.picker.cancel')}
          </Button>
        </div>
      </>
    );
  }

  return (
    <>
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.25rem',
          maxBlockSize: '24rem',
          overflowY: 'auto',
          border: '1px solid var(--nf-color-border)',
          borderRadius: '0.5rem',
        }}
      >
        {visible.map((ev) => (
          <PickerRow
            key={ev.id}
            event={ev}
            attached={attachedIds.has(ev.id)}
            checked={selected.has(ev.id)}
            onToggle={() => {
              toggle(ev.id);
            }}
          />
        ))}
      </ul>

      <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
        <Button type="button" variant="ghost" onClick={onClose} disabled={attach.isPending}>
          {t('workspace.public_shares.detail.picker.cancel')}
        </Button>
        <Button
          type="button"
          variant="primary"
          disabled={selected.size === 0 || attach.isPending}
          onClick={() => {
            void handleAttach();
          }}
        >
          {attach.isPending
            ? t('workspace.public_shares.detail.picker.attaching')
            : t('workspace.public_shares.detail.picker.attach', { count: selected.size })}
        </Button>
      </div>
    </>
  );
}

interface PickerRowProps {
  event: CrossCalendarEvent;
  attached: boolean;
  checked: boolean;
  onToggle: () => void;
}

function PickerRow({ event, attached, checked, onToggle }: PickerRowProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const locale = i18n.resolvedLanguage ?? 'en';
  const whenLabel = formatRange(event, locale, t('workspace.public_shares.detail.event_all_day'));
  const disabled = attached;
  return (
    <li
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
        padding: '0.5rem 0.75rem',
        opacity: disabled ? 0.55 : 1,
      }}
    >
      <Checkbox checked={checked} onChange={onToggle} disabled={disabled} />
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.125rem', flex: 1 }}>
        <span style={{ fontSize: '0.875rem', fontWeight: 500 }}>{event.title}</span>
        <span style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}>{whenLabel}</span>
      </div>
      {attached ? (
        <span
          style={{
            fontSize: '0.75rem',
            color: 'var(--nf-color-fg-muted)',
            border: '1px solid var(--nf-color-border)',
            borderRadius: '0.25rem',
            padding: '0.125rem 0.375rem',
          }}
        >
          {t('workspace.public_shares.detail.picker.already_attached')}
        </span>
      ) : null}
    </li>
  );
}

function formatRange(event: CrossCalendarEvent, locale: string, allDayLabel: string): string {
  if (!event.startAt) return '—';
  const start = new Date(event.startAt * 1000);
  const end = event.endAt ? new Date(event.endAt * 1000) : null;
  const dateFmt = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' });
  const timeFmt = new Intl.DateTimeFormat(locale, { timeStyle: 'short' });
  if (event.allDay) {
    if (!end || sameDay(start, end)) return `${dateFmt.format(start)} · ${allDayLabel}`;
    return `${dateFmt.format(start)} – ${dateFmt.format(end)}`;
  }
  if (!end) return `${dateFmt.format(start)} ${timeFmt.format(start)}`;
  if (sameDay(start, end)) {
    return `${dateFmt.format(start)} ${timeFmt.format(start)}–${timeFmt.format(end)}`;
  }
  return `${dateFmt.format(start)} ${timeFmt.format(start)} – ${dateFmt.format(end)} ${timeFmt.format(end)}`;
}

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}
