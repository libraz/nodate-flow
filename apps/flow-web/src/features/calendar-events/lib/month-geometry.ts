import type { Zone } from '@nodate-flow/ui/time';
import { dateKey, endOfDayIso, startOfDayIso } from '../../../lib/date-utils';
import styles from '../calendar-page.module.css';

export type WeekStart = 'mon' | 'sun' | 'sat';
type Weekday = 'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat' | 'sun';

/**
 * Weekday-key order indexed by the user's `weekStart` preference. The
 * column at index 0 is whichever day the user marked as the start of
 * their week; indexes 1..6 follow in calendar order.
 */
export const WEEKDAY_KEYS_BY_START: Record<WeekStart, readonly Weekday[]> = {
  mon: ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'],
  sun: ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'],
  sat: ['sat', 'sun', 'mon', 'tue', 'wed', 'thu', 'fri'],
};

/** JS `Date.getDay()` value (Sun=0..Sat=6) for the weekStart anchor. */
const WEEKSTART_TO_DOW: Record<WeekStart, number> = { sun: 0, mon: 1, sat: 6 };
/**
 * Day of week as 0..6 with the user's chosen start day as 0.
 * E.g. when `weekStart === 'sun'`, Sunday → 0, Monday → 1, …, Saturday → 6.
 */
export function dowFromStart(date: Date, weekStart: WeekStart): number {
  const anchor = WEEKSTART_TO_DOW[weekStart];
  return (date.getDay() - anchor + 7) % 7;
}

/**
 * Optional CSS class for the weekday header at column `index`. Looks at
 * the actual weekday key (sun → danger, sat → info), so the tint moves
 * with the user's start-of-week preference.
 */
export function weekdayHeaderClass(keys: readonly Weekday[], index: number): string | undefined {
  const key = keys[index];
  if (key === 'sun') return styles['weekday--sun'];
  if (key === 'sat') return styles['weekday--sat'];
  return undefined;
}

/**
 * Date-number CSS class for a calendar cell. Holidays take precedence
 * over the Saturday "info" tint so a Saturday-holiday reads as red,
 * matching the inline holiday label. Sundays and weekday-holidays
 * both resolve to danger; weekdays return `undefined` (caller keeps
 * default colour from `.dateNumber`).
 */
export function dateNumberClass(date: Date, hasHoliday: boolean): string | undefined {
  const dow = date.getDay();
  if (dow === 0 || hasHoliday) return styles['dateNumber--holiday'];
  if (dow === 6) return styles['dateNumber--sat'];
  return undefined;
}

/** Number of days in a given (year, monthIndex). */
export function daysInMonth(year: number, monthIndex: number): number {
  return new Date(year, monthIndex + 1, 0).getDate();
}

export interface MonthCell {
  date: Date;
  key: string;
  inMonth: boolean;
}

/**
 * Build a 6×7 month grid whose first column matches the user's
 * `weekStart`. Leading and trailing cells from adjacent months pad the
 * grid to a multiple of 7.
 */
export function buildMonthGrid(
  year: number,
  monthIndex: number,
  weekStart: WeekStart,
): MonthCell[] {
  const first = new Date(year, monthIndex, 1);
  const lead = dowFromStart(first, weekStart);
  const cells: MonthCell[] = [];
  for (let i = lead; i > 0; i--) {
    const d = new Date(year, monthIndex, 1 - i);
    cells.push({ date: d, key: dateKey(d), inMonth: false });
  }
  const total = daysInMonth(year, monthIndex);
  for (let day = 1; day <= total; day++) {
    const d = new Date(year, monthIndex, day);
    cells.push({ date: d, key: dateKey(d), inMonth: true });
  }
  while (cells.length % 7 !== 0) {
    const last = cells[cells.length - 1];
    if (!last) break;
    const d = new Date(last.date);
    d.setDate(d.getDate() + 1);
    cells.push({ date: d, key: dateKey(d), inMonth: false });
  }
  return cells;
}

/** The windows one calendar view fetches and draws from. */
export interface CalendarViewRange {
  /** Task query bounds — inclusive `YYYY-MM-DD` day keys. */
  fromDate: string;
  toDate: string;
  /** Event query bounds — the same days as instants, read in `zone`. */
  fromIso: string;
  toIso: string;
  /** The event bounds as `Date`s, for the recurrence expander. */
  rangeStart: Date;
  rangeEnd: Date;
  /** Holiday provider bounds, in local-component `Date`s. */
  holidayFrom: Date;
  holidayTo: Date;
}

/**
 * Every window the calendar reads, derived from the month at the cursor.
 *
 * `bufferMonths` widens the window by that many whole month grids on
 * each side. The mobile month view scrolls through months freely, and a
 * row reaches the top before the cursor that follows it can be fetched —
 * so the months on either side are asked for in advance, or they arrive
 * empty and fill in after the fact.
 *
 * All four windows are derived from one pair of days on purpose. A fetch
 * that reaches further than the expansion window loads occurrences the
 * expander then drops, and a holiday window that lags leaves the extra
 * months unmarked; either one is invisible until someone scrolls there.
 *
 * The grid's own cells are local-component Dates, so their day keys
 * round-trip in whatever frame they were built in. The instants sent as
 * `start`/`end` are a different question: they are the bounds of "the
 * days on screen", and a day boundary only exists relative to a zone.
 * Taken from the browser they ask the server for a window offset from
 * the one being drawn, so events at the first and last cell are fetched
 * for the wrong day — or, for a viewer far enough east or west, not
 * fetched at all.
 */
export function buildCalendarRange(
  cursor: { year: number; month: number },
  weekStart: WeekStart,
  zone: Zone,
  bufferMonths: number,
): CalendarViewRange {
  // `Date` normalises an out-of-range month index, so a buffer that
  // crosses a year boundary needs no special case.
  const leadingCells = buildMonthGrid(cursor.year, cursor.month - bufferMonths, weekStart);
  const trailingCells =
    bufferMonths === 0
      ? leadingCells
      : buildMonthGrid(cursor.year, cursor.month + bufferMonths, weekStart);
  const fallback = new Date(cursor.year, cursor.month, 1);
  const first = leadingCells[0]?.date ?? fallback;
  const last = trailingCells[trailingCells.length - 1]?.date ?? fallback;
  const firstKey = dateKey(first);
  const lastKey = dateKey(last);
  const startIso = startOfDayIso(firstKey, zone);
  const endIso = endOfDayIso(lastKey, zone);
  // The holiday window stays in local-component Dates because the
  // provider compares against local-component Dates of its own. A
  // holiday is a date, not an instant, so both sides being in the same
  // arbitrary frame is what makes the comparison mean "the same square"
  // — feeding it the zoned instants above would drop or add one at the
  // edge of the grid.
  const holidayStart = new Date(first);
  holidayStart.setHours(0, 0, 0, 0);
  const holidayEnd = new Date(last);
  holidayEnd.setHours(23, 59, 59, 999);
  return {
    fromDate: firstKey,
    toDate: lastKey,
    fromIso: startIso,
    toIso: endIso,
    rangeStart: new Date(startIso),
    rangeEnd: new Date(endIso),
    holidayFrom: holidayStart,
    holidayTo: holidayEnd,
  };
}

/** Local-component Date for a `YYYY-MM-DD` key, or null when it is not one. */
export function dateFromKey(key: string): Date | null {
  const [y, m, d] = key.split('-').map(Number);
  if (!y || !m || !d) return null;
  return new Date(y, m - 1, d);
}

/** Whole-day difference between two local `YYYY-MM-DD` keys (`to - from`). */
export function dayDeltaBetweenKeys(fromKey: string, toKey: string): number {
  const [fy, fm, fd] = fromKey.split('-').map(Number);
  const [ty, tm, td] = toKey.split('-').map(Number);
  if (!fy || !fm || !fd || !ty || !tm || !td) return 0;
  const from = new Date(fy, fm - 1, fd).getTime();
  const to = new Date(ty, tm - 1, td).getTime();
  return Math.round((to - from) / 86_400_000);
}

/** Which way the month grid last moved, for the direction it slides in from. */
export type NavDirection = 'prev' | 'next';

/** Direction from the month at `from` to the month at `to`, or null when they match. */
export function directionBetween(
  from: { year: number; month: number },
  to: { year: number; month: number },
): NavDirection | null {
  const delta = to.year * 12 + to.month - (from.year * 12 + from.month);
  if (delta === 0) return null;
  return delta < 0 ? 'prev' : 'next';
}

/**
 * Day offset an arrow / Home / End key moves the grid focus by, or null
 * for a key the grid does not handle. Inline arrows follow the writing
 * direction; Home and End stay within the focused cell's week row.
 */
export function keyDayDelta(key: string, dowIndex: number, rtl: boolean): number | null {
  switch (key) {
    case 'ArrowLeft':
      return rtl ? 1 : -1;
    case 'ArrowRight':
      return rtl ? -1 : 1;
    case 'ArrowUp':
      return -7;
    case 'ArrowDown':
      return 7;
    case 'Home':
      return -dowIndex;
    case 'End':
      return 6 - dowIndex;
    default:
      return null;
  }
}
