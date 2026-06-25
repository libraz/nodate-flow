/**
 * Week-row layout math for the mobile month-scroll view.
 *
 * Adapted from nodate-time's `week-layout.ts` but rebuilt for flow's
 * data model: calendar events carry `startAt` / `endAt` as unix-second
 * integers (not luxon ISO strings), and the layout works in the
 * browser's local timezone via plain `Date` objects — matching the
 * desktop month grid in `_authenticated.calendar.tsx`, which also keys
 * cells on local-time `YYYY-MM-DD`.
 *
 * Multi-day events are packed into non-overlapping horizontal tracks so
 * a continuous bar can stretch across the seven day columns of a single
 * week row. Single-day events are rendered separately by the caller into
 * the gaps those bars leave behind.
 */

import type { components } from '@nodate-flow/sdk';

import { dateKey } from '../../../lib/date-utils';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

/**
 * Maximum number of event tracks rendered per day cell before the
 * remainder collapses into a "+N" overflow indicator.
 */
export const MAX_VISIBLE_TRACKS = 3;

/** A multi-day event positioned within one Sunday/Monday-aligned week row. */
export interface PositionedEvent {
  event: CalendarEvent;
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

/** Midnight (local) `Date` for the day containing `unixSeconds`. */
function startOfDayFromUnix(unixSeconds: number): Date {
  const d = new Date(unixSeconds * 1000);
  d.setHours(0, 0, 0, 0);
  return d;
}

/** Midnight (local) `Date` for `d`. */
export function startOfDay(d: Date): Date {
  const out = new Date(d);
  out.setHours(0, 0, 0, 0);
  return out;
}

/** Whole-day difference `a - b` (both floored to local midnight). */
function dayDiff(a: Date, b: Date): number {
  const ms = startOfDay(a).getTime() - startOfDay(b).getTime();
  return Math.round(ms / 86_400_000);
}

/** Resolve an event's start as a local-midnight `Date`, or null if absent. */
export function eventStartDay(evt: CalendarEvent): Date | null {
  if (typeof evt.startAt !== 'number') return null;
  return startOfDayFromUnix(evt.startAt);
}

/** Resolve an event's end as a local-midnight `Date`, falling back to start. */
export function eventEndDay(evt: CalendarEvent): Date | null {
  if (typeof evt.endAt === 'number') return startOfDayFromUnix(evt.endAt);
  return eventStartDay(evt);
}

/** True when an event spans more than one calendar day in local time. */
export function isMultiDay(evt: CalendarEvent): boolean {
  const s = eventStartDay(evt);
  const e = eventEndDay(evt);
  if (!s || !e) return false;
  return dayDiff(e, s) > 0;
}

/** The local `YYYY-MM-DD` key for an event's start day, or null. */
export function eventStartKey(evt: CalendarEvent): string | null {
  const s = eventStartDay(evt);
  return s ? dateKey(s) : null;
}

/**
 * Pack the multi-day events that intersect `[weekStart, weekStart+6]`
 * into non-overlapping horizontal tracks. Each entry reports the column
 * span inside the week plus whether the bar is clipped on either edge.
 *
 * @param weekStart Local-midnight `Date` for the first day of the week.
 * @param events All visible events (single- and multi-day); single-day
 *   ones are ignored here and laid out by the caller.
 */
export function layoutWeek(weekStart: Date, events: CalendarEvent[]): PositionedEvent[] {
  const ws = startOfDay(weekStart);
  const we = startOfDay(new Date(ws.getTime() + 6 * 86_400_000));
  const tracks: { end: number }[] = [];
  const positioned: PositionedEvent[] = [];

  const multiDay = events.filter((e) => isMultiDay(e));
  multiDay.sort((a, b) => {
    const as = eventStartDay(a);
    const bs = eventStartDay(b);
    const am = as ? as.getTime() : 0;
    const bm = bs ? bs.getTime() : 0;
    return am - bm;
  });

  for (const evt of multiDay) {
    const evtStart = eventStartDay(evt);
    const evtEnd = eventEndDay(evt);
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
