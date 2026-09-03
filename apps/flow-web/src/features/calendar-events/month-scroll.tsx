/**
 * MonthScroll — mobile month view for `/calendar`.
 *
 * Renders an infinite vertical scroll of week rows aligned to the user's
 * `weekStart` preference, with a sticky month header that updates as the
 * user scrolls, and an initial auto-scroll that pins today's week just
 * under the header. It is a drop-in replacement for the desktop seven-
 * column `.grid` at narrow viewports — the calendar route swaps between
 * the two via {@link useIsMobile}.
 *
 * Data is fed in from the route (the same layer-filtered events, tasks,
 * and holidays the desktop grid consumes) so nothing is refetched. The
 * component is purely presentational + layout; all mutations (open
 * event, open task, reschedule) flow back through callbacks.
 *
 * All visual values resolve from design tokens; see the sibling CSS
 * module. Drag-to-move is desktop-only (HTML5 DnD is poor on touch), so
 * this surface is tap-to-open.
 */

import type { HolidayEntry } from '@nodate-flow/holidays';
import type { components } from '@nodate-flow/sdk';
import { cx } from '@nodate-flow/ui/lib/cx';
import { Day, type Zone } from '@nodate-flow/ui/time';
import { defaultRangeExtractor, useVirtualizer } from '@tanstack/react-virtual';
import { Users } from 'lucide-react';
import { type ReactElement, useCallback, useEffect, useLayoutEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { dateKey, todayKey as todayKeyIn } from '../../lib/date-utils';
import {
  eventStartKey,
  groupEventsByWeek,
  isMultiDay,
  layoutWeek,
  MAX_VISIBLE_TRACKS,
  type PositionedEvent,
  startOfDay,
} from './lib/week-layout';
import styles from './month-scroll.module.css';

type CalendarTask = components['schemas']['MyTaskListItem'];
type CalendarEvent = components['schemas']['MyCalendarEventResponse'];
type WeekStart = 'mon' | 'sun' | 'sat';

/** How many months of weeks to render before/after today. */
const RANGE_MONTHS = 12;

/** JS `Date.getDay()` value (Sun=0..Sat=6) for each weekStart anchor. */
const WEEKSTART_TO_DOW: Record<WeekStart, number> = { sun: 0, mon: 1, sat: 6 };

const MS_PER_DAY = 86_400_000;

/** Add `n` whole days to `d`, returning a fresh local-midnight `Date`. */
function addDays(d: Date, n: number): Date {
  return startOfDay(new Date(d.getTime() + n * MS_PER_DAY));
}

/** Day-of-week 0..6 with the user's chosen start day mapped to 0. */
function dowFromStart(date: Date, weekStart: WeekStart): number {
  return (date.getDay() - WEEKSTART_TO_DOW[weekStart] + 7) % 7;
}

type ScrollItem =
  | { kind: 'header'; key: string; monthKey: string; date: Date }
  | { kind: 'week'; key: string; weekStart: Date };

/**
 * Build the flat list of month-header + week-row items covering
 * `RANGE_MONTHS` before and after today, plus the week key that
 * contains today (so the view can auto-scroll there on mount).
 */
function buildItems(
  weekStart: WeekStart,
  zone: Zone,
): {
  items: ScrollItem[];
  todayWeekKey: string;
  todayIndex: number;
  headerIndexes: number[];
  weekStarts: Date[];
} {
  // The skeleton is laid out in local-component Dates, but *which* day is
  // today is a zone question, and it decides both the month range and the
  // week the view auto-scrolls to on mount. Taken from the browser it can
  // name a different week from the one the highlight lands in, so the
  // view opens scrolled past the day it marks.
  const todayDay = Day.today(zone);
  const today = new Date(todayDay.year, todayDay.month - 1, todayDay.day);
  const rangeStart = new Date(today.getFullYear(), today.getMonth() - RANGE_MONTHS, 1);
  const rangeEnd = new Date(today.getFullYear(), today.getMonth() + RANGE_MONTHS + 1, 0);

  const items: ScrollItem[] = [];
  const headerIndexes: number[] = [];
  const weekStarts: Date[] = [];
  const seenMonths = new Set<string>();
  let todayWeekKey = '';
  let todayIndex = 0;

  // First week-start on or before the range start.
  let ws = addDays(rangeStart, -dowFromStart(rangeStart, weekStart));

  while (ws.getTime() <= rangeEnd.getTime()) {
    // Emit a month header for any month whose 1st falls inside this week,
    // and always one for the very first week so the list opens with a
    // header.
    for (let i = 0; i < 7; i++) {
      const d = addDays(ws, i);
      const isFirstOfMonth = d.getDate() === 1;
      if (isFirstOfMonth || (items.length === 0 && i === 0)) {
        const anchor = isFirstOfMonth ? d : new Date(d.getFullYear(), d.getMonth(), 1);
        const monthKey = `${anchor.getFullYear()}-${String(anchor.getMonth() + 1).padStart(2, '0')}`;
        if (!seenMonths.has(monthKey)) {
          seenMonths.add(monthKey);
          headerIndexes.push(items.length);
          items.push({ kind: 'header', key: `h-${monthKey}`, monthKey, date: anchor });
        }
      }
    }

    const weekKey = `w-${dateKey(ws)}`;
    const weekEndExclusive = addDays(ws, 7);
    if (today.getTime() >= ws.getTime() && today.getTime() < weekEndExclusive.getTime()) {
      todayWeekKey = dateKey(ws);
      todayIndex = items.length;
    }
    weekStarts.push(ws);
    items.push({ kind: 'week', key: weekKey, weekStart: ws });
    ws = addDays(ws, 7);
  }

  return { items, todayWeekKey, todayIndex, headerIndexes, weekStarts };
}

/** Date-number tint for a day cell (Sunday/holiday → danger, Saturday → info). */
function dateNumberClass(date: Date, hasHoliday: boolean): string | undefined {
  const dow = date.getDay();
  if (dow === 0 || hasHoliday) return styles['dayNumber--holiday'];
  if (dow === 6) return styles['dayNumber--sat'];
  return undefined;
}

/** Per-kind pill accent colour token (matches the desktop grid's markers). */
function markerColorForKind(kind: string): string {
  switch (kind) {
    case 'block':
      return 'var(--nf-cal-block-color)';
    case 'free':
      return 'var(--nf-cal-free-color)';
    case 'milestone':
      return 'var(--nf-cal-milestone-color)';
    default:
      return 'var(--nf-cal-event-color)';
  }
}

export interface MonthScrollProps {
  /** Layer-filtered calendar events (single- and multi-day). */
  events: CalendarEvent[];
  /** Local `YYYY-MM-DD` → tasks due that day (already layer-filtered). */
  tasksByDate: Map<string, CalendarTask[]>;
  /** Local `YYYY-MM-DD` → public holidays that day. */
  holidaysByDate: Map<string, HolidayEntry[]>;
  /** Resolved BCP-47 locale for the month-header label. */
  locale: string;
  /** User's start-of-week preference. */
  weekStart: WeekStart;
  /**
   * Effective zone (profile, else workspace, else browser). Decides
   * which calendar day a timed event falls on, so the mobile view files
   * events under the same days the desktop grid and the server-side
   * reminders do.
   */
  zone: Zone;
  /** State colour lookup for task dots, keyed by `derivedState`. */
  stateColor: (derivedState: string) => string;
  /** Bumped by the toolbar "Today" button to request a re-scroll. */
  scrollToTodaySignal: number;
  /** Open the unified create dialog for an empty cell. */
  onDayCreate: (dateKey: string) => void;
  /** Open an event in edit mode. */
  onEventOpen: (event: CalendarEvent) => void;
  /** Open a task detail. */
  onTaskOpen: (task: CalendarTask) => void;
}

/** Vertical metrics (rem) that keep single-day chips aligned with multi-day bars. */
const TRACK_REM = 1.25;

/** Shared empty list so weeks with no events keep a stable prop identity. */
const EMPTY_EVENTS: CalendarEvent[] = [];

interface WeekRowProps {
  weekStart: Date;
  /** Effective zone; see MonthScrollProps.zone. */
  zone: Zone;
  events: CalendarEvent[];
  tasksByDate: Map<string, CalendarTask[]>;
  holidaysByDate: Map<string, HolidayEntry[]>;
  todayKey: string;
  stateColor: (derivedState: string) => string;
  onDayCreate: (dateKey: string) => void;
  onEventOpen: (event: CalendarEvent) => void;
  onTaskOpen: (task: CalendarTask) => void;
}

function WeekRow({
  weekStart,
  zone,
  events,
  tasksByDate,
  holidaysByDate,
  todayKey,
  stateColor,
  onDayCreate,
  onEventOpen,
  onTaskOpen,
}: WeekRowProps): ReactElement {
  const { t } = useTranslation('common');
  const week = useMemo(
    () =>
      Array.from({ length: 7 }, (_, i) =>
        startOfDay(new Date(weekStart.getTime() + i * MS_PER_DAY)),
      ),
    [weekStart],
  );

  const positioned = useMemo(() => layoutWeek(weekStart, events, zone), [weekStart, events, zone]);

  // Single-day events grouped by date key within this week, read in the
  // effective zone.
  const singleDayMap = useMemo(() => {
    const map = new Map<string, CalendarEvent[]>();
    const weekKeys = new Set(week.map((d) => dateKey(d)));
    for (const evt of events) {
      if (isMultiDay(evt, zone)) continue;
      const key = eventStartKey(evt, zone);
      if (!key || !weekKeys.has(key)) continue;
      const arr = map.get(key);
      if (arr) arr.push(evt);
      else map.set(key, [evt]);
    }
    return map;
  }, [events, week, zone]);

  // Tracks reserved by multi-day bars for each day column.
  const reservedByCol = useMemo(() => {
    return week.map((_, col) => {
      const reserved = new Set<number>();
      for (const p of positioned) {
        if (col >= p.startCol && col < p.startCol + p.span) reserved.add(p.track);
      }
      return reserved;
    });
  }, [week, positioned]);

  const tracksUsed = Math.min(
    MAX_VISIBLE_TRACKS,
    positioned.reduce((max, p) => Math.max(max, p.track + 1), 0),
  );

  return (
    <div className={styles.weekRow} data-week={dateKey(weekStart)}>
      {/* Multi-day bar overlay sits above the day columns. */}
      {tracksUsed > 0 ? (
        <div className={styles.barOverlay} style={{ blockSize: `${tracksUsed * TRACK_REM}rem` }}>
          {positioned.map((p: PositionedEvent) => {
            if (p.track >= MAX_VISIBLE_TRACKS) return null;
            const insetStart = `calc(${(p.startCol * 100) / 7}% + var(--nf-space-px))`;
            const width = `calc(${(p.span * 100) / 7}% - var(--nf-space-1))`;
            return (
              <button
                key={`${p.event.id}-${p.startCol}`}
                type="button"
                className={cx(
                  styles.bar,
                  p.continuesLeft && styles['bar--clipStart'],
                  p.continuesRight && styles['bar--clipEnd'],
                )}
                style={{
                  insetInlineStart: insetStart,
                  inlineSize: width,
                  insetBlockStart: `${p.track * TRACK_REM}rem`,
                  background: `color-mix(in oklch, ${markerColorForKind(p.event.kind)} 22%, transparent)`,
                  borderInlineStartColor: markerColorForKind(p.event.kind),
                }}
                title={`${p.event.title} · ${p.event.workspaceName}`}
                aria-label={t('calendar.event_detail.open_label', {
                  title: p.event.title,
                  workspace: p.event.workspaceName,
                })}
                onClick={() => onEventOpen(p.event)}
              >
                <span className={styles.barTitle}>{p.event.title}</span>
              </button>
            );
          })}
        </div>
      ) : null}

      <div className={styles.dayCols}>
        {week.map((day, col) => {
          const key = dateKey(day);
          const isToday = key === todayKey;
          const dayHolidays = holidaysByDate.get(key) ?? [];
          const primaryHoliday = dayHolidays[0] ?? null;
          const dayTasks = tasksByDate.get(key) ?? [];
          const reserved = reservedByCol[col] ?? new Set<number>();

          // Place single-day chips into the tracks the bars left free.
          const singles = singleDayMap.get(key) ?? [];
          const slots: { track: number; evt: CalendarEvent }[] = [];
          const used = new Set(reserved);
          let next = 0;
          for (const evt of singles) {
            while (used.has(next)) next++;
            slots.push({ track: next, evt });
            used.add(next);
            next++;
          }

          const totalItems = reserved.size + singles.length + dayTasks.length;
          const shownTracks = Math.min(MAX_VISIBLE_TRACKS, tracksUsed);
          const overflow = Math.max(0, totalItems - shownTracks - dayTasks.length);

          // Resolve each visible track to either a chip (single-day
          // event) or a spacer (reserved by a multi-day bar, or empty).
          // Keys embed the cell date so they are stable and unique.
          const trackCells = Array.from({ length: shownTracks }, (_, track) => {
            if (reserved.has(track)) {
              return { kind: 'spacer' as const, key: `${key}-bar-${track}` };
            }
            const slot = slots.find((s) => s.track === track);
            if (!slot) return { kind: 'spacer' as const, key: `${key}-gap-${track}` };
            return { kind: 'chip' as const, key: slot.evt.id, evt: slot.evt };
          });

          return (
            <div key={key} className={styles.dayCol} data-cell-key={key}>
              <button
                type="button"
                className={cx(styles.dayHead, isToday && styles['dayHead--today'])}
                onClick={() => onDayCreate(key)}
                aria-label={
                  primaryHoliday
                    ? t('calendar.month_scroll.day_label_holiday', {
                        date: key,
                        holiday: primaryHoliday.name,
                      })
                    : t('calendar.month_scroll.day_label', { date: key })
                }
              >
                <span
                  className={cx(
                    styles.dayNumber,
                    dateNumberClass(day, dayHolidays.length > 0),
                    isToday && styles['dayNumber--today'],
                  )}
                >
                  {day.getDate()}
                </span>
              </button>

              {primaryHoliday ? (
                <span className={styles.holidayLabel} title={primaryHoliday.name}>
                  {primaryHoliday.name}
                </span>
              ) : null}

              {/* Reserve vertical space so single-day chips line up under bars. */}
              <div
                className={styles.trackArea}
                style={{ blockSize: `${shownTracks * TRACK_REM}rem` }}
              >
                {trackCells.map((tc) => {
                  if (tc.kind === 'spacer') {
                    return <div key={tc.key} className={styles.trackSpacer} />;
                  }
                  const evt = tc.evt;
                  return (
                    <button
                      key={tc.key}
                      type="button"
                      className={styles.chip}
                      style={{
                        background: `color-mix(in oklch, ${markerColorForKind(evt.kind)} 18%, transparent)`,
                        borderInlineStartColor: markerColorForKind(evt.kind),
                      }}
                      title={`${evt.title} · ${evt.workspaceName}`}
                      aria-label={t('calendar.event_detail.open_label', {
                        title: evt.title,
                        workspace: evt.workspaceName,
                      })}
                      onClick={() => onEventOpen(evt)}
                    >
                      <span className={styles.chipTitle}>{evt.title}</span>
                      {evt.attendeeCount > 0 ? (
                        <span aria-hidden className={styles.chipAttendees}>
                          <Users className={styles.chipAttendeesIcon} />
                          {evt.attendeeCount}
                        </span>
                      ) : null}
                    </button>
                  );
                })}
              </div>

              {/* Task dots row — tasks render below events as compact chips. */}
              {dayTasks.length > 0 ? (
                <ul className={styles.taskList}>
                  {dayTasks.slice(0, 2).map((task) => (
                    <li key={task.id}>
                      <button
                        type="button"
                        className={styles.taskChip}
                        title={`${task.title} · ${task.workspaceName}`}
                        onClick={() => onTaskOpen(task)}
                      >
                        <span
                          aria-hidden
                          className={styles.taskDot}
                          style={{ background: stateColor(task.derivedState) }}
                        />
                        <span className={styles.taskTitle}>{task.title}</span>
                      </button>
                    </li>
                  ))}
                  {dayTasks.length > 2 ? (
                    <li className={styles.more}>
                      {t('calendar.more', { count: dayTasks.length - 2 })}
                    </li>
                  ) : null}
                </ul>
              ) : null}

              {overflow > 0 ? (
                <span className={styles.more}>{t('calendar.more', { count: overflow })}</span>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** Starting height guesses (px) the virtualizer refines by measuring. */
const ESTIMATED_HEADER_PX = 32;
const ESTIMATED_WEEK_PX = 112;

/**
 * How far below the top of the viewport "scroll to today" leaves the
 * week — enough to clear the pinned month header.
 */
const TODAY_SCROLL_INSET_PX = 32;

/**
 * MonthScroll — top-level infinite-scroll month view. Builds the week
 * range once on mount (today never moves within a session) and renders
 * weekday labels, a scrollable body of week rows with sticky month
 * headers, and auto-scrolls to today on mount + whenever the toolbar
 * "Today" signal changes.
 *
 * The two years of rows are virtualized: only the rows near the viewport
 * are in the DOM, instead of a hundred-odd rows and their several
 * hundred day cells all mounted at once for a phone to lay out. The
 * month header for the row at the top stays pinned through the
 * virtualizer's range, since a `position: sticky` element cannot be one
 * of the absolutely-positioned rows around it.
 */
export default function MonthScroll({
  events,
  tasksByDate,
  holidaysByDate,
  locale,
  weekStart,
  zone,
  stateColor,
  scrollToTodaySignal,
  onDayCreate,
  onEventOpen,
  onTaskOpen,
}: MonthScrollProps): ReactElement {
  const { t } = useTranslation('common');
  const scrollRef = useRef<HTMLDivElement>(null);

  const { items, todayIndex, headerIndexes, weekStarts } = useMemo(
    () => buildItems(weekStart, zone),
    [weekStart, zone],
  );
  // "Today" is read in the effective zone, the same one the events are
  // filed under. Read from the browser instead, the highlight lands on a
  // different cell from the pills whenever the two zones are on opposite
  // sides of midnight — the mobile view saying one day and the desktop
  // grid, which already reads the effective zone, saying another.
  const todayKey = useMemo(() => todayKeyIn(zone), [zone]);

  // One pass over the events instead of one pass per rendered week.
  const eventsByWeek = useMemo(
    () => groupEventsByWeek(events, weekStarts, dateKey, zone),
    [events, weekStarts, zone],
  );

  const weekdayLabels = useMemo(() => {
    const fmt = new Intl.DateTimeFormat(locale, { weekday: 'short' });
    // 2023-01-01 is a Sunday; offset by the weekStart anchor.
    const base = new Date(2023, 0, 1 + WEEKSTART_TO_DOW[weekStart]);
    return Array.from({ length: 7 }, (_, i) => {
      const d = new Date(base.getTime() + i * MS_PER_DAY);
      return { label: fmt.format(d), dow: d.getDay() };
    });
  }, [locale, weekStart]);

  const monthFmt = useMemo(
    () => new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }),
    [locale],
  );

  // Index of the month header currently pinned at the top. Recomputed
  // from the virtualizer's own range so it tracks the scroll position
  // without a second scroll listener.
  const activeHeaderRef = useRef(headerIndexes[0] ?? 0);

  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (index) =>
      items[index]?.kind === 'header' ? ESTIMATED_HEADER_PX : ESTIMATED_WEEK_PX,
    overscan: 3,
    rangeExtractor: (range) => {
      // The header for the month the top row belongs to is always kept
      // in the range, whether or not it is inside the window: it is the
      // one rendered as the pinned header.
      let active = headerIndexes[0] ?? 0;
      for (const index of headerIndexes) {
        if (index > range.startIndex) break;
        active = index;
      }
      activeHeaderRef.current = active;
      const indexes = new Set(defaultRangeExtractor(range));
      indexes.add(active);
      return [...indexes].sort((a, b) => a - b);
    },
  });

  const scrollToToday = useCallback(
    (smooth: boolean) => {
      if (!todayIndex) return;
      virtualizer.scrollToIndex(todayIndex, {
        align: 'start',
        behavior: smooth ? 'smooth' : 'auto',
      });
      const container = scrollRef.current;
      // Back the row off the very top so the pinned header does not
      // cover it, matching where the view has always opened.
      if (container) container.scrollTop -= TODAY_SCROLL_INSET_PX;
    },
    [todayIndex, virtualizer],
  );

  // Initial position: align today's week just under the pinned header.
  useLayoutEffect(() => {
    scrollToToday(false);
  }, [scrollToToday]);

  // Toolbar "Today" button bumps the signal; re-scroll on an actual change.
  const lastSignal = useRef(scrollToTodaySignal);
  useEffect(() => {
    if (lastSignal.current === scrollToTodaySignal) return;
    lastSignal.current = scrollToTodaySignal;
    scrollToToday(true);
  }, [scrollToTodaySignal, scrollToToday]);

  return (
    <div className={styles.root}>
      <div className={styles.weekdayRow} aria-hidden>
        {weekdayLabels.map((w) => (
          <div
            key={w.label}
            className={cx(
              styles.weekday,
              w.dow === 0 && styles['weekday--sun'],
              w.dow === 6 && styles['weekday--sat'],
            )}
          >
            {w.label}
          </div>
        ))}
      </div>

      <div
        ref={scrollRef}
        className={styles.scrollBody}
        role="grid"
        aria-label={t('calendar.month_scroll.aria_label')}
      >
        <div className={styles.scrollInner} style={{ blockSize: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((virtualItem) => {
            const item = items[virtualItem.index];
            if (!item) return null;
            const pinned = virtualItem.index === activeHeaderRef.current;
            // The pinned header is the one element that must not be
            // translated into place — sticky positioning and a transform
            // cannot both apply to it.
            const style = pinned ? undefined : { transform: `translateY(${virtualItem.start}px)` };
            if (item.kind === 'header') {
              return (
                <div
                  key={item.key}
                  ref={virtualizer.measureElement}
                  data-index={virtualItem.index}
                  className={cx(styles.monthHeader, !pinned && styles.virtualItem)}
                  data-month={item.monthKey}
                  style={style}
                >
                  {monthFmt.format(item.date)}
                </div>
              );
            }
            return (
              <div
                key={item.key}
                ref={virtualizer.measureElement}
                data-index={virtualItem.index}
                className={styles.virtualItem}
                style={style}
              >
                <WeekRow
                  weekStart={item.weekStart}
                  zone={zone}
                  events={eventsByWeek.get(dateKey(item.weekStart)) ?? EMPTY_EVENTS}
                  tasksByDate={tasksByDate}
                  holidaysByDate={holidaysByDate}
                  todayKey={todayKey}
                  stateColor={stateColor}
                  onDayCreate={onDayCreate}
                  onEventOpen={onEventOpen}
                  onTaskOpen={onTaskOpen}
                />
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
