/**
 * Month-grid layout math for the public calendar share.
 *
 * This is a self-contained port of the authenticated calendar's
 * `features/calendar-events/lib/week-layout.ts`: it keeps the same
 * multi-day track-packing algorithm (events that intersect a week row
 * are packed into non-overlapping horizontal tracks so a continuous bar
 * can stretch across the seven day columns), but is retyped against the
 * public share's event shape (`PublicShareRenderEvent`) so the public
 * bundle never depends on authenticated-only fields (`workspaceName`,
 * `attendeeCount`, creator/owner data).
 *
 * Unlike the authenticated view, day boundaries here are resolved in the
 * share's *publishing* timezone (not the visitor's local zone) so the
 * grid matches the timezone label shown on the page regardless of where
 * the anonymous visitor is located.
 */

import type { components } from '@nodate-flow/sdk';
import { expandAllRecurrences, type RecurrenceRule } from '@nodate-flow/ui/calendar';
import { DateTime } from 'luxon';

type ShareEvent = components['schemas']['PublicShareRenderEvent'];
type RecurrenceExpansionInput<T> = {
  event: T;
  startAt: string;
  endAt: string;
  timezone: string;
  recurrenceRule: RecurrenceRule | null;
  recurrenceExceptions?: string[];
};

/**
 * Maximum number of event tracks rendered per day cell before the
 * remainder collapses into a "+N" overflow indicator. Matches the
 * authenticated grid for visual parity.
 */
export const MAX_VISIBLE_TRACKS = 3;

/** JS `Date.getDay()` value (Sun=0..Sat=6) for each week-start anchor. */
const WEEKSTART_TO_DOW = { sun: 0, mon: 1, sat: 6 } as const;

/** Supported start-of-week anchors. */
export type WeekStart = keyof typeof WEEKSTART_TO_DOW;

const MS_PER_DAY = 86_400_000;

/** A multi-day event positioned within one week row of the month grid. */
export interface PositionedEvent {
  event: ShareEvent;
  /** Column index (0..6) where the bar starts inside this week. */
  startCol: number;
  /** Number of columns the bar spans (>= 1). */
  span: number;
  /** Track index the bar occupies (0-based, top to bottom). */
  track: number;
  /** True when the event began before this week (left-clipped). */
  continuesLeft: boolean;
  /** True when the event ends after this week (right-clipped). */
  continuesRight: boolean;
}

/** One day cell in the month grid. */
export interface DayCell {
  /** Zoned `YYYY-MM-DD` key (stable, locale-independent). */
  key: string;
  /** Day-of-month number (1..31). */
  dayNumber: number;
  /** True when this cell falls in the month being displayed. */
  inMonth: boolean;
  /** True when this cell is "today" in the share timezone. */
  isToday: boolean;
  /** JS day-of-week (Sun=0..Sat=6) for weekend tinting. */
  dow: number;
  /** Stable sort timestamp (zoned-midnight epoch ms) for layout math. */
  epochDay: number;
}

/** One week row: seven cells plus the multi-day bars packed across it. */
export interface WeekRow {
  /** `YYYY-MM-DD` key of the first cell (stable React key). */
  key: string;
  cells: DayCell[];
  bars: PositionedEvent[];
}

/** A fully laid-out month: header label inputs plus week rows. */
export interface MonthGrid {
  /** First day of the displayed month, at zoned midnight (for labels). */
  monthAnchor: Date;
  weekdayOrder: number[];
  weeks: WeekRow[];
}

/**
 * Zoned `YYYY-MM-DD` for `date` in `zone`. Uses the `en-CA` short-date
 * format (always `YYYY-MM-DD`) so the key is unaffected by the visitor's
 * display locale. Falls back to the UTC date on malformed zones.
 */
function zonedDateKey(date: Date, zone: string): string {
  try {
    const fmt = new Intl.DateTimeFormat('en-CA', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      timeZone: zone,
    });
    return fmt.format(date);
  } catch {
    return date.toISOString().slice(0, 10);
  }
}

/** Parse a `YYYY-MM-DD` key into a UTC-midnight `Date` (a stable day token). */
function keyToUtcMidnight(key: string): Date {
  const [y, m, d] = key.split('-').map((p) => Number.parseInt(p, 10));
  return new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1));
}

/** Add `n` whole days to a UTC-midnight day token. */
function addDays(d: Date, n: number): Date {
  return new Date(d.getTime() + n * MS_PER_DAY);
}

function monthGridBounds(
  monthAnchorKey: string,
  weekStart: WeekStart,
): {
  firstOfMonth: Date;
  gridStart: Date;
  gridEndExclusive: Date;
} {
  const anchor = keyToUtcMidnight(monthAnchorKey);
  const firstOfMonth = new Date(Date.UTC(anchor.getUTCFullYear(), anchor.getUTCMonth(), 1));
  const startDow = WEEKSTART_TO_DOW[weekStart];
  const lead = (firstOfMonth.getUTCDay() - startDow + 7) % 7;
  const gridStart = addDays(firstOfMonth, -lead);
  return { firstOfMonth, gridStart, gridEndExclusive: addDays(gridStart, 42) };
}

/** Whole-day difference `a - b` between two UTC-midnight day tokens. */
function dayDiff(a: Date, b: Date): number {
  return Math.round((a.getTime() - b.getTime()) / MS_PER_DAY);
}

/** Resolve an event's start day as a zoned UTC-midnight token, or null. */
function eventStartDay(evt: ShareEvent, zone: string): Date | null {
  if (typeof evt.startAt !== 'number') return null;
  return keyToUtcMidnight(zonedDateKey(new Date(evt.startAt * 1000), zone));
}

/** Resolve an event's end day, falling back to its start day. */
function eventEndDay(evt: ShareEvent, zone: string): Date | null {
  if (typeof evt.endAt === 'number') {
    return keyToUtcMidnight(zonedDateKey(new Date(evt.endAt * 1000), zone));
  }
  return eventStartDay(evt, zone);
}

/** True when an event spans more than one calendar day in `zone`. */
export function isMultiDay(evt: ShareEvent, zone: string): boolean {
  const s = eventStartDay(evt, zone);
  const e = eventEndDay(evt, zone);
  if (!s || !e) return false;
  return dayDiff(e, s) > 0;
}

/** The zoned `YYYY-MM-DD` key for an event's start day, or null. */
export function eventStartKey(evt: ShareEvent, zone: string): string | null {
  if (typeof evt.startAt !== 'number') return null;
  return zonedDateKey(new Date(evt.startAt * 1000), zone);
}

/**
 * Pack the multi-day events intersecting `[weekStart, weekStart+6]` into
 * non-overlapping horizontal tracks. Ported verbatim (algorithmically)
 * from the authenticated `layoutWeek`, retyped for the share event.
 *
 * @param weekStart UTC-midnight day token for the first day of the week.
 */
export function layoutWeek(weekStart: Date, events: ShareEvent[], zone: string): PositionedEvent[] {
  const ws = weekStart;
  const we = addDays(ws, 6);
  const tracks: { end: number }[] = [];
  const positioned: PositionedEvent[] = [];

  const multiDay = events.filter((e) => isMultiDay(e, zone));
  multiDay.sort((a, b) => {
    const as = eventStartDay(a, zone);
    const bs = eventStartDay(b, zone);
    return (as ? as.getTime() : 0) - (bs ? bs.getTime() : 0);
  });

  for (const evt of multiDay) {
    const evtStart = eventStartDay(evt, zone);
    const evtEnd = eventEndDay(evt, zone);
    if (!evtStart || !evtEnd) continue;
    if (evtEnd.getTime() < ws.getTime() || evtStart.getTime() > we.getTime()) continue;

    const visStart = evtStart.getTime() < ws.getTime() ? ws : evtStart;
    const visEnd = evtEnd.getTime() > we.getTime() ? we : evtEnd;

    const startCol = Math.max(0, dayDiff(visStart, ws));
    const endCol = Math.min(6, dayDiff(visEnd, ws));
    const span = Math.max(1, endCol - startCol + 1);

    let track = tracks.findIndex((tr) => tr.end < startCol);
    if (track < 0) {
      track = tracks.length;
      tracks.push({ end: endCol });
    } else {
      tracks[track] = { end: endCol };
    }

    positioned.push({
      event: evt,
      startCol,
      span,
      track,
      continuesLeft: evtStart.getTime() < ws.getTime(),
      continuesRight: evtEnd.getTime() > we.getTime(),
    });
  }

  return positioned;
}

/** Today's zoned `YYYY-MM-DD` key. */
export function todayKey(zone: string): string {
  return zonedDateKey(new Date(), zone);
}

/**
 * Build the full six-week month grid for the month containing
 * `monthAnchorKey` (a `YYYY-MM-DD`), aligned to `weekStart`. Day cells
 * carry their zoned key, weekend/today tinting hints, and the packed
 * multi-day bars for each week row.
 */
export function buildMonthGrid(
  monthAnchorKey: string,
  events: ShareEvent[],
  zone: string,
  weekStart: WeekStart,
): MonthGrid {
  const { firstOfMonth, gridStart } = monthGridBounds(monthAnchorKey, weekStart);
  const monthIndex = firstOfMonth.getUTCMonth();
  const startDow = WEEKSTART_TO_DOW[weekStart];

  const tnow = todayKey(zone);
  const weeks: WeekRow[] = [];

  for (let w = 0; w < 6; w++) {
    const weekStartDay = addDays(gridStart, w * 7);
    const cells: DayCell[] = [];
    for (let i = 0; i < 7; i++) {
      const day = addDays(weekStartDay, i);
      const key = `${day.getUTCFullYear()}-${String(day.getUTCMonth() + 1).padStart(2, '0')}-${String(
        day.getUTCDate(),
      ).padStart(2, '0')}`;
      cells.push({
        key,
        dayNumber: day.getUTCDate(),
        inMonth: day.getUTCMonth() === monthIndex,
        isToday: key === tnow,
        dow: day.getUTCDay(),
        epochDay: day.getTime(),
      });
    }
    weeks.push({
      key: cells[0]?.key ?? `week-${w}`,
      cells,
      bars: layoutWeek(weekStartDay, events, zone),
    });
  }

  const weekdayOrder = Array.from({ length: 7 }, (_, i) => (startDow + i) % 7);

  return { monthAnchor: firstOfMonth, weekdayOrder, weeks };
}

/** `YYYY-MM` (zoned) for the month that contains the share's first event. */
export function monthKeyOf(dateKey: string): string {
  return dateKey.slice(0, 7);
}

/** Step a `YYYY-MM-DD` month anchor by `n` months (clamped to the 1st). */
export function shiftMonthAnchor(monthAnchorKey: string, n: number): string {
  const d = keyToUtcMidnight(monthAnchorKey);
  const next = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + n, 1));
  return `${next.getUTCFullYear()}-${String(next.getUTCMonth() + 1).padStart(2, '0')}-01`;
}

function parseJsonMaybe(value: unknown): unknown {
  if (typeof value !== 'string') return value;
  const trimmed = value.trim();
  if (!trimmed || trimmed === 'null') return null;
  try {
    return JSON.parse(trimmed) as unknown;
  } catch {
    return null;
  }
}

function recurrenceRule(value: unknown): RecurrenceRule | null {
  const parsed = parseJsonMaybe(value);
  if (!parsed || typeof parsed !== 'object') return null;
  const freq = (parsed as { freq?: unknown }).freq;
  if (freq !== 'daily' && freq !== 'weekly' && freq !== 'monthly' && freq !== 'yearly') return null;
  return parsed as RecurrenceRule;
}

function recurrenceExceptions(value: unknown): string[] | undefined {
  const parsed = parseJsonMaybe(value);
  if (!Array.isArray(parsed)) return undefined;
  const strings = parsed.filter((v): v is string => typeof v === 'string');
  return strings.length > 0 ? strings : undefined;
}

function secondsToIso(seconds: number): string {
  return new Date(seconds * 1000).toISOString();
}

function isoToSeconds(iso: string): number | undefined {
  const millis = Date.parse(iso);
  if (Number.isNaN(millis)) return undefined;
  return Math.floor(millis / 1000);
}

/** Expand recurring public-share event masters into concrete visible instances. */
export function expandShareEventsForMonth(
  monthAnchorKey: string,
  events: ShareEvent[],
  zone: string,
  weekStart: WeekStart,
): ShareEvent[] {
  const { gridStart, gridEndExclusive } = monthGridBounds(monthAnchorKey, weekStart);
  const recurrenceInput: RecurrenceExpansionInput<ShareEvent>[] = events.map((event) => {
    const rule = recurrenceRule(event.recurrenceRule);
    const exceptions = recurrenceExceptions(event.recurrenceExceptions);
    const input: RecurrenceExpansionInput<ShareEvent> = {
      event,
      startAt: typeof event.startAt === 'number' ? secondsToIso(event.startAt) : '',
      endAt:
        typeof event.endAt === 'number'
          ? secondsToIso(event.endAt)
          : typeof event.startAt === 'number'
            ? secondsToIso(event.startAt)
            : '',
      timezone: event.timezone || zone,
      recurrenceRule: rule,
    };
    if (exceptions) input.recurrenceExceptions = exceptions;
    return input;
  });

  return expandAllRecurrences(
    recurrenceInput,
    DateTime.fromJSDate(gridStart, { zone: 'utc' }),
    DateTime.fromJSDate(gridEndExclusive, { zone: 'utc' }),
  ).map((instance) => ({
    ...instance.event,
    ...((isoToSeconds(instance.startAt) ?? instance.event.startAt)
      ? { startAt: isoToSeconds(instance.startAt) ?? instance.event.startAt }
      : {}),
    ...((isoToSeconds(instance.endAt) ?? instance.event.endAt)
      ? { endAt: isoToSeconds(instance.endAt) ?? instance.event.endAt }
      : {}),
  }));
}
