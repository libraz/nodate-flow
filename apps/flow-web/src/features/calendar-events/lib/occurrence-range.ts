import type { Zone } from '@nodate-flow/ui/time';
import { allDayLastDateKey, unixToWallClock } from '../../../lib/date-utils';

/**
 * The instants an edit form should open on.
 *
 * A series is stored once, as its first occurrence, while the grid draws
 * one pill per instance the rule produces. Seeded from the stored row,
 * every instance therefore opens on the series' own dates: click the
 * meeting on 14 October and the fields read 16 September, and saving
 * that range is how an occurrence gets moved back onto the master's day.
 * The occurrence carries the instant the rule gave it, so the form opens
 * there and keeps the stored duration — the one part of the range the
 * occurrence does not itself state.
 *
 * The shift is arithmetic on instants rather than on wall clocks so
 * all-day and timed rows stay on a single path: an all-day span is a
 * whole number of days in seconds, and moving both ends by the same
 * amount preserves it either way.
 *
 * `occurrenceStartAt` is only ever {@link DialogOccurrence.originalStartAt}
 * — the start the rule produced, never the start the form is holding.
 * Passing the latter would shift the range onto itself and call the
 * result an occurrence.
 */
export function occurrenceRange(
  stored: { startAt?: number | null; endAt?: number | null },
  occurrenceStartAt: number | null,
): { startAt: number | null; endAt: number | null } {
  const startAt = stored.startAt ?? null;
  const endAt = stored.endAt ?? null;
  if (occurrenceStartAt === null || startAt === null) return { startAt, endAt };
  return {
    startAt: occurrenceStartAt,
    endAt: endAt === null ? null : occurrenceStartAt + (endAt - startAt),
  };
}

/**
 * The end date the form shows for a stored range. An all-day row stores
 * the day after its last day, read in UTC; a timed row ends where it ends.
 */
export function displayEndDate(
  range: { startAt: number | null; endAt: number | null },
  allDay: boolean,
  zone: Zone,
): string {
  if (range.endAt === null) return '';
  if (allDay && range.startAt !== null) return allDayLastDateKey(range.startAt, range.endAt);
  return unixToWallClock(range.endAt, zone).date;
}
