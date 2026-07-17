import { DateTime } from 'luxon';

import type { RecurrenceRule } from './types';

interface RecurrenceEvent {
  startAt: string;
  endAt: string;
  timezone?: string | undefined;
  recurrenceRule: RecurrenceRule | null;
  recurrenceExceptions?: string[] | undefined;
}

interface ExpandedInstance {
  startAt: DateTime;
  endAt: DateTime;
}

const DAY_MAP: Record<string, number> = {
  mo: 1,
  tu: 2,
  we: 3,
  th: 4,
  fr: 5,
  sa: 6,
  su: 7,
};

/**
 * Compute occurrence `n` of a rule directly from the DTSTART anchor.
 *
 * Every candidate is derived as `anchor + n * interval` in the freq unit,
 * never from the previous occurrence: chaining `plus({ months })` off an
 * already-clamped value would turn Jan 31 -> Feb 28 -> Mar 28 and drift every
 * later month to the 28th. Anchor-based arithmetic lets luxon clamp each
 * occurrence independently (Jan 31 -> Feb 28 -> Mar 31 -> Apr 30), matching
 * RFC 5545 expansion of a monthly rule anchored on day 31. The same applies
 * to yearly rules anchored on Feb 29.
 */
function occurrenceFromAnchor(
  anchor: DateTime,
  freq: RecurrenceRule['freq'],
  offset: number,
): DateTime {
  switch (freq) {
    case 'daily':
      return anchor.plus({ days: offset });
    case 'weekly':
      return anchor.plus({ weeks: offset });
    case 'monthly':
      return anchor.plus({ months: offset });
    case 'yearly':
      return anchor.plus({ years: offset });
  }
}

function matchesByDay(dt: DateTime, byDay: string[]): boolean {
  const isoWeekday = dt.weekday;
  return byDay.some((d) => DAY_MAP[d.trim().toLowerCase()] === isoWeekday);
}

function matchesByMonthDay(dt: DateTime, byMonthDay: number[]): boolean {
  return byMonthDay.includes(dt.day);
}

/**
 * Number of whole ISO weeks (Monday start) between the anchor's week and the
 * candidate's week, in the event timezone. Used to decide whether a day
 * belongs to an "included" week of a WEEKLY;INTERVAL=n rule. Rounded because
 * a DST transition can make the wall-clock span between two week starts a
 * non-integral number of 7-day blocks.
 */
function isoWeekOffset(candidate: DateTime, anchorWeekStart: DateTime): number {
  return Math.round(candidate.startOf('week').diff(anchorWeekStart, 'weeks').weeks);
}

interface RecurrenceExceptionSets {
  instants: Set<number>;
  localDayKeys: Set<string>;
}

// Parse an ISO timestamp and anchor it to `zone` if provided, so subsequent
// calendar arithmetic preserves wall-clock time across DST transitions.
// Returns an invalid DateTime if parsing fails.
function parseInZone(iso: string, zone: string | undefined): DateTime {
  return zone ? DateTime.fromISO(iso, { zone }) : DateTime.fromISO(iso);
}

/**
 * Parse an RFC 5545 UNTIL value as an inclusive upper bound.
 *
 * A date-only UNTIL means "through the end of that local day". Parsing it at
 * local midnight would exclude every timed occurrence landing on the UNTIL
 * day itself, so widen bare dates to `endOf('day')`. Datetime values stay an
 * exact inclusive instant.
 */
function parseUntil(until: string, zone: string | undefined): DateTime {
  const parsed = parseInZone(until, zone);
  if (/^\d{4}-\d{2}-\d{2}$/.test(until.trim())) {
    return parsed.endOf('day');
  }
  return parsed;
}

function buildRecurrenceExceptionSets(
  values: string[] | undefined,
  zone: string | undefined,
): RecurrenceExceptionSets {
  const instants = new Set<number>();
  const localDayKeys = new Set<string>();

  for (const raw of values ?? []) {
    const value = raw.trim();
    if (!value) continue;
    if (/^\d{4}-\d{2}-\d{2}$/.test(value)) {
      localDayKeys.add(value);
      continue;
    }

    const withOffset = DateTime.fromISO(value, { setZone: true });
    const parsed = withOffset.isValid ? withOffset : parseInZone(value, zone);
    if (parsed.isValid) {
      instants.add(parsed.toMillis());
    }
  }

  return { instants, localDayKeys };
}

/** Expand a recurring event into concrete instances within `[rangeStart, rangeEnd)`. */
export function expandRecurrence(
  event: RecurrenceEvent,
  rangeStart: DateTime,
  rangeEnd: DateTime,
): ExpandedInstance[] {
  const rule = event.recurrenceRule;
  if (!rule) return [];

  const zone = event.timezone || undefined;
  const eventStart = parseInZone(event.startAt, zone);
  const eventEnd = parseInZone(event.endAt, zone);
  const duration = eventEnd.diff(eventStart);
  const interval = rule.interval ?? 1;
  const until = rule.until ? parseUntil(rule.until, zone) : null;
  const maxCount = rule.count ?? Number.POSITIVE_INFINITY;
  const exceptions = buildRecurrenceExceptionSets(event.recurrenceExceptions, zone);

  const byDay = rule.byDay && rule.byDay.length > 0 ? rule.byDay : null;
  const byMonthDay = rule.byMonthDay && rule.byMonthDay.length > 0 ? rule.byMonthDay : null;

  // RFC 5545 BYDAY *expands* a WEEKLY rule: WEEKLY;BYDAY=MO,TU,WE,TH,FR
  // yields one occurrence per listed weekday in every included week, not just
  // the DTSTART weekday. That path scans day by day and keeps a day when its
  // weekday is listed and its ISO-week offset from DTSTART is a multiple of
  // the interval. For every other supported combination byDay/byMonthDay act
  // as limits on the freq cursor.
  const expandsWeekdays = rule.freq === 'weekly' && byDay !== null;
  const anchorWeekStart = eventStart.startOf('week');

  const results: ExpandedInstance[] = [];
  let emitted = 0;

  for (let n = 0; emitted < maxCount; n++) {
    const candidate = expandsWeekdays
      ? eventStart.plus({ days: n })
      : occurrenceFromAnchor(eventStart, rule.freq, n * interval);

    // Negated comparison so an invalid candidate or range still terminates.
    if (!(candidate < rangeEnd)) break;
    if (until && candidate > until) break;

    const passes = expandsWeekdays
      ? matchesByDay(candidate, byDay) &&
        isoWeekOffset(candidate, anchorWeekStart) % interval === 0 &&
        (!byMonthDay || matchesByMonthDay(candidate, byMonthDay))
      : (!byDay || matchesByDay(candidate, byDay)) &&
        (!byMonthDay || matchesByMonthDay(candidate, byMonthDay));

    if (passes) {
      emitted++;
      const excluded =
        exceptions.instants.has(candidate.toMillis()) ||
        exceptions.localDayKeys.has(candidate.toFormat('yyyy-MM-dd'));
      if (!excluded) {
        const instanceEnd = candidate.plus(duration);
        if (instanceEnd > rangeStart) {
          results.push({ startAt: candidate, endAt: instanceEnd });
        }
      }
    }
  }

  return results;
}

/** Expand all recurring events in a list, merging with non-recurring events. */
export function expandAllRecurrences<
  T extends {
    startAt: string;
    endAt: string;
    timezone?: string;
    recurrenceRule?: RecurrenceRule | null;
    recurrenceExceptions?: string[];
  },
>(events: T[], rangeStart: DateTime, rangeEnd: DateTime): T[] {
  const result: T[] = [];

  for (const event of events) {
    if (!event.recurrenceRule) {
      result.push(event);
      continue;
    }

    const instances = expandRecurrence(
      {
        startAt: event.startAt,
        endAt: event.endAt,
        timezone: event.timezone,
        recurrenceRule: event.recurrenceRule,
        recurrenceExceptions: event.recurrenceExceptions,
      },
      rangeStart,
      rangeEnd,
    );

    for (const instance of instances) {
      result.push({
        ...event,
        startAt: instance.startAt.toISO() ?? '',
        endAt: instance.endAt.toISO() ?? '',
      });
    }
  }

  return result;
}
