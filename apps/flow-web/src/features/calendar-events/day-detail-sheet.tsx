/**
 * DayDetailSheet — one day of the phone month view, opened in full.
 *
 * The month grid packs a whole week of days into a phone width, which
 * puts its chips well under a usable touch target. Rather than grow them
 * — halving how many a day can show, when at-a-glance density is the
 * grid's whole job — the grid is not where a day is operated on. A tap
 * anywhere in a day column opens it here, where every row is a target a
 * finger can hit and where the event dialog, the task, and the create
 * gesture the cell used to carry all live.
 *
 * The sheet is a {@link Drawer} on the block-end edge rather than a
 * bottom sheet of its own: the primitive already owns the overlay lock,
 * Escape routing and the focus trap, and the route mounts it for the
 * calendars rail already.
 *
 * Rows are ordered all-day, then timed by clock, then tasks — the order
 * a day is read in, not the order the caller happened to pass. All
 * visual values resolve from design tokens; see the sibling CSS module.
 */

import type { HolidayEntry } from '@nodate-flow/holidays';
import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import type { Zone } from '@nodate-flow/ui/time';
import { type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './day-detail-sheet.module.css';
import { markerColorForKind } from './lib/kind-color';

type CalendarTask = components['schemas']['MyTaskListItem'];
type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

export interface DayDetailSheetProps {
  /** Local `YYYY-MM-DD` for the day being shown. */
  dateKey: string;
  /** Resolved BCP-47 locale for the header and the row times. */
  locale: string;
  /**
   * Effective zone (profile, else workspace, else browser). A row's time
   * is a wall clock, and a wall clock only means something in a zone —
   * read from the browser it would disagree with the day the grid filed
   * the event under.
   */
  zone: Zone;
  /** Every event covering this day, single- and multi-day alike. */
  events: CalendarEvent[];
  /** Tasks due this day. */
  tasks: CalendarTask[];
  /** Public holidays on this day, if the layer is on. */
  holidays: HolidayEntry[];
  /** State colour lookup for task markers, keyed by `derivedState`. */
  stateColor: (derivedState: string) => string;
  onClose: () => void;
  /** Open an event in edit mode. */
  onEventOpen: (event: CalendarEvent) => void;
  /** Open a task detail. */
  onTaskOpen: (task: CalendarTask) => void;
  /** Create an event on this day — the gesture the day cell gave up. */
  onCreate: (dateKey: string) => void;
}

/** Local-midnight `Date` for a `YYYY-MM-DD`, for the header formatter. */
function dateFromKey(key: string): Date {
  const [y, m, d] = key.split('-').map(Number);
  return new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1);
}

/** A row of the sheet, already resolved to what it draws. */
interface DayRow {
  key: string;
  /** Wall-clock start, an all-day marker, or the task's due marker. */
  timeLabel: string;
  title: string;
  workspaceName: string;
  markerColor: string;
  onOpen: () => void;
}

export default function DayDetailSheet({
  dateKey,
  locale,
  zone,
  events,
  tasks,
  holidays,
  stateColor,
  onClose,
  onEventOpen,
  onTaskOpen,
  onCreate,
}: DayDetailSheetProps): ReactElement {
  const { t } = useTranslation('common');

  const dayLabel = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        weekday: 'long',
        year: 'numeric',
        month: 'long',
        day: 'numeric',
      }).format(dateFromKey(dateKey)),
    [locale, dateKey],
  );

  const timeFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { timeStyle: 'short', timeZone: zone.name }),
    [locale, zone],
  );

  const rows = useMemo<DayRow[]>(() => {
    const allDay: DayRow[] = [];
    // Kept beside the row so the order is decided by the instant rather
    // than by the formatted label, which on a 12-hour clock sorts the
    // afternoon before the morning.
    const timed: { at: number; row: DayRow }[] = [];

    for (const evt of events) {
      const isAllDay = evt.allDay === true || typeof evt.startAt !== 'number';
      const row: DayRow = {
        key: `event-${evt.id}`,
        timeLabel: isAllDay
          ? t('calendar.day_detail.all_day')
          : timeFormat.format((evt.startAt ?? 0) * 1000),
        title: evt.title,
        workspaceName: evt.workspaceName,
        markerColor: markerColorForKind(evt.kind),
        onOpen: () => onEventOpen(evt),
      };
      if (isAllDay) allDay.push(row);
      else timed.push({ at: evt.startAt ?? 0, row });
    }
    timed.sort((a, b) => a.at - b.at);

    const taskRows = tasks.map<DayRow>((task) => ({
      key: `task-${task.id}`,
      // A task is due on a date, not at a time, so the slot the events
      // put a clock in says what kind of row this is instead.
      timeLabel: t('calendar.day_detail.task_due'),
      title: task.title,
      workspaceName: task.workspaceName,
      markerColor: stateColor(task.derivedState),
      onOpen: () => onTaskOpen(task),
    }));

    return [...allDay, ...timed.map((entry) => entry.row), ...taskRows];
  }, [events, tasks, timeFormat, stateColor, onEventOpen, onTaskOpen, t]);

  const holidayName = holidays[0]?.name ?? null;

  return (
    <Drawer
      open
      onClose={onClose}
      side="block-end"
      data-testid="day-detail-sheet"
      title={
        <span className={styles.heading}>
          <span>{dayLabel}</span>
          {holidayName !== null ? <span className={styles.holiday}>{holidayName}</span> : null}
        </span>
      }
    >
      {rows.length === 0 ? (
        <EmptyState titleAs="p" title={t('calendar.day_detail.empty')} />
      ) : (
        <ul className={styles.list}>
          {rows.map((row) => (
            <li key={row.key}>
              <button type="button" className={styles.row} onClick={row.onOpen}>
                <span
                  aria-hidden
                  className={styles.marker}
                  style={{ background: row.markerColor }}
                />
                <span className={styles.time}>{row.timeLabel}</span>
                <span className={styles.rowBody}>
                  <span className={styles.rowTitle}>{row.title}</span>
                  <span className={styles.rowMeta}>{row.workspaceName}</span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      <Button
        type="button"
        variant="primary"
        className={styles.create}
        onClick={() => onCreate(dateKey)}
      >
        {t('calendar.day_detail.create')}
      </Button>
    </Drawer>
  );
}
