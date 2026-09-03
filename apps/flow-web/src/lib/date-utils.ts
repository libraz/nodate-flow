/**
 * Wall-clock helpers for the surfaces that build or read a calendar day.
 *
 * Every function that crosses between an instant and a calendar day
 * takes a resolved {@link Zone}, with no default. The default is exactly
 * the bug: a day boundary only means something relative to a zone, and
 * the one the browser happens to be in is the reader's, not the data's.
 * A `zone?: string` parameter can always be left off, and every call
 * site that leaves it off silently answers in whoever is looking.
 */

import { Day, Zone } from '@nodate-flow/ui/time';
import { DateTime } from 'luxon';

/**
 * `YYYY-MM-DD` read from a `Date`'s local components.
 *
 * Only for `Date` values that were themselves built from local
 * components — the month grid constructs its cells that way, so building
 * and reading them in the same frame round-trips exactly. An instant
 * that came from the server must go through {@link eventDateKey}
 * instead, which asks which zone to read it in.
 */
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
export function eventDateKey(unixSeconds: number, allDay: boolean, zone: Zone): string {
  if (allDay) {
    const d = new Date(unixSeconds * 1000);
    const y = d.getUTCFullYear();
    const m = String(d.getUTCMonth() + 1).padStart(2, '0');
    const day = String(d.getUTCDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
  }
  return DateTime.fromSeconds(unixSeconds, { zone: zone.name }).toFormat('yyyy-MM-dd');
}

/**
 * Local midnight `Date` for the day an event's instant belongs to, using
 * the same frame rule as [eventDateKey].
 *
 * The returned Date is always built from local components, because the
 * grid it feeds lays out local day columns; only the choice of *which*
 * calendar day is made in the event's frame.
 */
export function eventStartOfDay(unixSeconds: number, allDay: boolean, zone: Zone): Date {
  const key = eventDateKey(unixSeconds, allDay, zone);
  const [y, m, day] = key.split('-').map(Number);
  return new Date(y ?? 1970, (m ?? 1) - 1, day ?? 1, 0, 0, 0, 0);
}

/**
 * `YYYY-MM-DD` for "today" in `zone`.
 *
 * The highlight on a grid has to agree with the day the events are filed
 * under. Taken from the browser it lands on the wrong cell for anyone
 * whose effective zone crosses midnight differently — visible as a
 * calendar that highlights yesterday while the pills sit on today.
 */
export function todayKey(zone: Zone, now: Date = new Date()): string {
  return DateTime.fromJSDate(now, { zone: zone.name }).toFormat('yyyy-MM-dd');
}

/**
 * Unix seconds for a `YYYY-MM-DD` date and an `HH:MM` wall clock, read
 * in `zone`.
 *
 * This is the write boundary of every time control in the product: what
 * the user typed is a wall clock, and it only becomes an instant once a
 * zone is named. Naming the browser's while the request declares another
 * one produces a stored instant that contradicts the timezone stamped
 * beside it — the value looks right to whoever saved it and is wrong for
 * everybody else.
 *
 * A date or time that does not parse yields the start of the epoch day
 * in `zone` rather than "now", so an upstream bug surfaces as an
 * obviously wrong value instead of a plausible one.
 */
export function wallClockToUnix(dayKey: string, hhmm: string, zone: Zone): number {
  const day = Day.parse(dayKey) ?? Day.of(1970, 1, 1);
  const [rawHour, rawMinute] = hhmm.split(':').map(Number);
  const hour = Number.isFinite(rawHour) ? (rawHour as number) : 0;
  const minute = Number.isFinite(rawMinute) ? (rawMinute as number) : 0;
  return Math.floor(day.at(zone, hour, minute).toSeconds());
}

/**
 * The `YYYY-MM-DD` and `HH:MM` a unix instant shows as in `zone` — the
 * inverse of {@link wallClockToUnix}.
 *
 * Seeding an edit form and submitting it are one round trip, so they
 * have to name the same zone. Reading in the browser and writing in the
 * effective zone (or the reverse) shifts every event that is merely
 * opened and saved by the offset between them, which is a silent edit of
 * data the user never touched.
 */
export function unixToWallClock(unixSeconds: number, zone: Zone): { date: string; time: string } {
  const local = DateTime.fromSeconds(unixSeconds, { zone: zone.name });
  return { date: local.toFormat('yyyy-MM-dd'), time: local.toFormat('HH:mm') };
}

/**
 * Midnight UTC, as unix seconds, for a `YYYY-MM-DD` date.
 *
 * All-day events are dates, and a date is the same square on the
 * calendar wherever you are. Sending local midnight made it an instant
 * that lands on a different date for anyone whose offset crosses it: a
 * Tokyo user's company holiday arrived as 15:00Z the day before and read
 * as the 4th in Europe. The API normalises to midnight UTC on the way
 * in, so sending anything else only means the value echoed back is not
 * the one that was sent.
 */
export function allDayToUnix(dayKey: string): number {
  const day = Day.parse(dayKey) ?? Day.of(1970, 1, 1);
  return Math.floor(day.start(Zone.utc()).toSeconds());
}

/**
 * ISO instant for the first moment of `dayKey` in `zone`.
 *
 * The range parameters the calendar endpoints take are instants, so the
 * bounds of "the days on screen" are a zone question. Built in the
 * browser's zone they ask the server for a window offset from the one
 * being drawn, and the events at the seam are fetched for the wrong day
 * or not at all.
 */
export function startOfDayIso(dayKey: string, zone: Zone): string {
  const day = Day.parse(dayKey) ?? Day.of(1970, 1, 1);
  return day.start(zone).toUTC().toISO() ?? '';
}

/**
 * ISO instant for the last representable moment of `dayKey` in `zone`.
 *
 * One millisecond before the next day starts, so the range stays
 * inclusive of its final day the way the endpoints already expect,
 * without an end bound that reaches into the day after.
 */
export function endOfDayIso(dayKey: string, zone: Zone): string {
  const day = Day.parse(dayKey) ?? Day.of(1970, 1, 1);
  return day.endExclusive(zone).minus({ milliseconds: 1 }).toUTC().toISO() ?? '';
}
