/**
 * Which day cells a calendar event occupies.
 *
 * Lives beside the week-row layout rather than in the route, because the
 * desktop month grid and the mobile month-scroll have to agree on it and
 * previously did not: the grid filed each event under its start day
 * alone, so a five-day absence showed as a single Monday entry with
 * Tuesday through Friday reading as free, while the same account on a
 * phone saw the whole bar.
 */

import type { components } from '@nodate-flow/sdk';
import type { Zone } from '@nodate-flow/ui/time';

import { dateKey, eventStartOfDay } from '../../../lib/date-utils';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

/**
 * Upper bound on the number of cells one event may occupy. A month grid
 * shows at most six weeks, so a longer span is a data problem rather
 * than a long holiday — and without a cap, an end date far in the future
 * turns building one month's map into a hang.
 */
export const MAX_EVENT_SPAN_DAYS = 42;

/**
 * Every `YYYY-MM-DD` cell an event covers, from its first day through
 * its last.
 *
 * A single-day event yields one key, which is what the grid always did.
 * A multi-day event yields one per day, which is what it did not.
 *
 * All-day events are read in UTC and timed events in the effective
 * timezone, following [eventStartOfDay]: an all-day row is a date and
 * has to name the same square for every viewer, while a timed one falls
 * on whichever day the reader's own zone puts it.
 */
export function eventDayKeys(event: CalendarEvent, zone: Zone): string[] {
  if (typeof event.startAt !== 'number') return [];
  const allDay = event.allDay === true;
  const start = eventStartOfDay(event.startAt, allDay, zone);
  const endSeconds = typeof event.endAt === 'number' ? event.endAt : event.startAt;
  const end = eventStartOfDay(endSeconds, allDay, zone);

  const keys = [dateKey(start)];
  const cursor = new Date(start);
  for (let i = 0; i < MAX_EVENT_SPAN_DAYS && cursor < end; i++) {
    cursor.setDate(cursor.getDate() + 1);
    if (cursor > end) break;
    keys.push(dateKey(cursor));
  }
  return keys;
}
