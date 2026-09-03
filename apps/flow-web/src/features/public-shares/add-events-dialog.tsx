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
import { Day, type Zone } from '@nodate-flow/ui/time';
import { type ReactElement, Suspense, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { endOfDayIso, startOfDayIso } from '../../lib/date-utils';
import { formatDate } from '../../lib/format';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import { useWeekStart } from '../../lib/use-week-start';
import styles from './add-events-dialog.module.css';
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

/**
 * The default [today, +90 days] window, as `YYYY-MM-DD` strings in the
 * effective zone.
 *
 * Which day "today" is has to match the zone the range is later resolved
 * in, or the picker opens on a window that starts a day off its own
 * label for anyone whose browser sits on the other side of midnight.
 */
export function defaultRange(zone: Zone): { from: string; to: string } {
  const today = Day.today(zone);
  return { from: today.toString(), to: today.addDays(90).toString() };
}

export default function AddEventsDialog({
  workspaceId,
  shareId,
  open,
  attachedIds,
  onClose,
}: AddEventsDialogProps): ReactElement {
  const { t } = useTranslation('settings');
  const weekStart = useWeekStart();
  const { t: tCommon, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = tCommon('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });
  // The zone the range bounds are resolved in. Built in the browser's,
  // the request asked for a window offset from the one the picker
  // labelled: `new Date('2026-03-01T00:00:00').toISOString()` is local
  // midnight reinterpreted as UTC, so a viewer in Tokyo asked for
  // 2026-02-28T15:00Z and lost the events on the day they selected. The
  // same strings are the query cache key, so the drift was cached too.
  const zone = useEffectiveZone();
  const defaults = useMemo(() => defaultRange(zone), [zone]);
  const [from, setFrom] = useState(defaults.from);
  const [to, setTo] = useState(defaults.to);

  return (
    <Dialog open={open} onClose={onClose} title={t('workspace.public_shares.detail.picker.title')}>
      <div className={styles.body}>
        <div className={styles.rangeRow}>
          <FormField
            label={t('workspace.public_shares.detail.picker.range_from')}
            className={styles.rangeField}
          >
            {() => (
              <DatePicker
                value={from}
                onChange={setFrom}
                weekdayLabels={weekdayLabels}
                weekStart={weekStart}
                formatMonthYear={formatMonthYear}
                prevLabel={tCommon('calendar.prev')}
                nextLabel={tCommon('calendar.next')}
                triggerLabel={from ? formatDate(from, locale) : tCommon('common.date.placeholder')}
              />
            )}
          </FormField>
          <FormField
            label={t('workspace.public_shares.detail.picker.range_to')}
            className={styles.rangeField}
          >
            {() => (
              <DatePicker
                value={to}
                onChange={setTo}
                weekdayLabels={weekdayLabels}
                weekStart={weekStart}
                formatMonthYear={formatMonthYear}
                prevLabel={tCommon('calendar.prev')}
                nextLabel={tCommon('calendar.next')}
                triggerLabel={to ? formatDate(to, locale) : tCommon('common.date.placeholder')}
                {...(from ? { minDate: from } : {})}
              />
            )}
          </FormField>
        </div>

        <p className={styles.hint}>{t('workspace.public_shares.detail.picker.hint')}</p>

        <Suspense
          fallback={
            <div className={styles.skeletonStack}>
              <Skeleton className={styles.skeletonRow} />
              <Skeleton className={styles.skeletonRow} />
              <Skeleton className={styles.skeletonRow} />
            </div>
          }
        >
          <PickerBody
            workspaceId={workspaceId}
            shareId={shareId}
            startIso={startOfDayIso(from, zone)}
            endIso={endOfDayIso(to, zone)}
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
        <p className={styles.empty}>{t('workspace.public_shares.detail.picker.no_candidates')}</p>
        <div className={styles.endActions}>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t('workspace.public_shares.detail.picker.cancel')}
          </Button>
        </div>
      </>
    );
  }

  return (
    <>
      <ul className={styles.candidateList}>
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

      <div className={styles.endActions}>
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
    <li className={`${styles.row} ${disabled ? styles.rowDisabled : ''}`.trim()}>
      <Checkbox checked={checked} onChange={onToggle} disabled={disabled} />
      <div className={styles.rowIdentity}>
        <span className={styles.eventTitle}>{event.title}</span>
        <span className={styles.eventWhen}>{whenLabel}</span>
      </div>
      {attached ? (
        <span className={styles.attachedBadge}>
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
