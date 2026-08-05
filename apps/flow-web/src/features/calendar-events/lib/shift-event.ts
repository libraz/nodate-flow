/**
 * Day-resolution moves for calendar events, as the month grid's drag
 * and drop performs them.
 *
 * Extracted from the calendar route so the arithmetic can be tested on
 * its own: the bug it replaces is invisible except on two days a year,
 * which is exactly the kind a rendered-component test never reaches.
 */

import { DateTime } from 'luxon';

/** The fields a day-shift needs from an event row. */
export interface ShiftableEvent {
  startAt?: number | undefined;
  endAt?: number | undefined;
  timezone?: string | undefined;
}

/**
 * Shift an event's start and end by whole calendar days, preserving both
 * its duration and its time of day.
 *
 * "A day" is not 86,400 seconds. On the two days a year a zone changes
 * offset it is 23 or 25 hours, so adding a fixed number of seconds moves
 * the wall clock: dragging a 10:00 meeting across the spring transition
 * saved it at 11:00 — for the organiser and every attendee, with no
 * warning and nothing in the UI to notice. Calendar-day arithmetic in
 * the event's zone keeps 10:00 at 10:00.
 *
 * The zone is the event's own, not the viewer's. A meeting owned in
 * Tokyo keeps its Tokyo wall-clock time when a colleague in Berlin drags
 * it to another day; taking the dragger's zone would quietly re-time it
 * for everyone else on the invitation.
 *
 * Returns null for an undated (planning-stage) event, which has no day
 * to move.
 */
export function shiftEventDays(
  event: ShiftableEvent,
  dayDelta: number,
): { startAt: number; endAt: number } | null {
  if (typeof event.startAt !== 'number') return null;
  const zone = event.timezone || 'UTC';
  const shift = (seconds: number): number =>
    Math.floor(DateTime.fromSeconds(seconds, { zone }).plus({ days: dayDelta }).toSeconds());
  return {
    startAt: shift(event.startAt),
    endAt: shift(typeof event.endAt === 'number' ? event.endAt : event.startAt),
  };
}
