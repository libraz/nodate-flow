import { DateTime } from 'luxon';

/** Local-time YYYY-MM-DD for the start of `d`. */
export function dateKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/**
 * `YYYY-MM-DD` for a calendar event's instant, read in the frame that
 * instant belongs to.
 *
 * Two frames, because the column means two things.
 *
 * All-day events are dates: "5 August" is the same square for everyone,
 * the API stores them at midnight UTC so there is one answer, and
 * reading them in anybody's local zone makes it two again — a Tokyo
 * user's company holiday reappears on the 4th in Europe.
 *
 * Timed events are instants, and which day 14:00Z falls on genuinely
 * depends on where you are. "Where you are" is the effective timezone
 * (profile, else workspace, else browser), not the browser's: someone
 * from Tokyo working in Berlin has said which day boundaries they want,
 * and the server's reminders already use that answer.
 */
export function eventDateKey(unixSeconds: number, allDay: boolean, zone?: string): string {
  if (allDay) {
    const d = new Date(unixSeconds * 1000);
    const y = d.getUTCFullYear();
    const m = String(d.getUTCMonth() + 1).padStart(2, '0');
    const day = String(d.getUTCDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
  }
  if (zone) {
    return DateTime.fromSeconds(unixSeconds, { zone }).toFormat('yyyy-MM-dd');
  }
  return dateKey(new Date(unixSeconds * 1000));
}

/**
 * Local midnight `Date` for the day an event's instant belongs to, using
 * the same frame rule as [eventDateKey].
 *
 * The returned Date is always built from local components, because the
 * grid it feeds lays out local day columns; only the choice of *which*
 * calendar day is made in the event's frame.
 */
export function eventStartOfDay(unixSeconds: number, allDay: boolean, zone?: string): Date {
  const key = eventDateKey(unixSeconds, allDay, zone);
  const [y, m, day] = key.split('-').map(Number);
  return new Date(y ?? 1970, (m ?? 1) - 1, day ?? 1, 0, 0, 0, 0);
}

/**
 * `YYYY-MM-DD` for "today" in the effective timezone.
 *
 * The highlight on the grid has to agree with the day the events are
 * filed under. Taken from the browser it lands on the wrong cell for
 * anyone whose profile zone crosses midnight differently — visible as a
 * calendar that highlights yesterday.
 */
export function todayKey(zone?: string, now: Date = new Date()): string {
  if (zone) {
    return DateTime.fromJSDate(now, { zone }).toFormat('yyyy-MM-dd');
  }
  return dateKey(now);
}
