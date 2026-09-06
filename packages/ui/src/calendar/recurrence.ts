import { DateTime } from 'luxon';

import { Zone } from '../time';
import type { RecurrenceRule } from './types';

interface RecurrenceEvent {
  startAt: string;
  endAt: string;
  timezone?: string | undefined;
  recurrenceRule: RecurrenceRule | null;
  recurrenceExceptions?: string[] | undefined;
  /**
   * The occurrences a separate override row already stands in for: the
   * `recurrence_original_start` of every row naming this event in
   * `recurrence_parent_id`. Same two shapes as `recurrenceExceptions`.
   *
   * A second input rather than more entries in `recurrenceExceptions`,
   * because the two say different things about the same occurrence: an
   * exception says it does not happen, an overridden start says it
   * happens elsewhere and the override row draws it. Merged into one
   * list the expander could no longer tell a consumer which, and the
   * master would still have to suppress both. Left unread the master
   * emits the original occurrence while the override row renders at its
   * moved time, so the same occurrence appears twice.
   */
  overriddenStarts?: string[] | undefined;
  /**
   * The `recurrence_end` column: an inclusive last instant for the
   * series, stored beside the rule rather than inside it.
   *
   * A second upper bound, not a replacement for the rule's own UNTIL,
   * and whichever comes first ends the series. Ignoring it — which this
   * expander did — meant a series the API had been told to stop kept
   * being drawn, on the authenticated calendar and on the public share
   * page alike, with no way to end it from the UI.
   *
   * The Go expander (packages/go-shared/recurrence) reads the same
   * column the same way; the two implementations serve the same stored
   * rules and a divergence between them is a bug in whichever one
   * differs.
   */
  recurrenceEnd?: string | undefined;
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

// Parse an ISO timestamp and anchor it to `zone`, so subsequent calendar
// arithmetic preserves wall-clock time across DST transitions.
// Returns an invalid DateTime if parsing fails.
//
// The zone is a resolved [Zone] rather than an optional string on
// purpose. Falling back to the browser's zone is what luxon does when
// given a bare `fromISO`, and it made the expansion a property of who
// was looking: wall-clock arithmetic ran in the viewer's zone, so a
// weekly series spanning that viewer's DST transition slid by an hour
// for them and not for anyone else, and a bare-date exception cancelled
// a different occurrence in every zone.
function parseInZone(iso: string, zone: Zone): DateTime {
  return DateTime.fromISO(iso, { zone: zone.name });
}

/**
 * Parse an RFC 5545 UNTIL value as an inclusive upper bound.
 *
 * A date-only UNTIL means "through the end of that local day". Parsing it at
 * local midnight would exclude every timed occurrence landing on the UNTIL
 * day itself, so widen bare dates to `endOf('day')`. Datetime values stay an
 * exact inclusive instant.
 */
function parseUntil(until: string, zone: Zone): DateTime {
  const parsed = parseInZone(until, zone);
  if (/^\d{4}-\d{2}-\d{2}$/.test(until.trim())) {
    return parsed.endOf('day');
  }
  return parsed;
}

/**
 * Whether an ISO timestamp states its own UTC offset, either as `Z` or as
 * `±HH:MM` / `±HHMM`.
 *
 * Luxon's `setZone` only takes effect when the string carries an offset;
 * given a bare local timestamp it silently falls back to the system zone.
 * The two cases therefore have to be told apart before parsing, because
 * the fallback is never the reading we want.
 */
function hasExplicitOffset(value: string): boolean {
  return /(?:Z|[+-]\d{2}:?\d{2})$/i.test(value);
}

/**
 * Split a stored list of occurrence starts into the two kinds it mixes:
 * exact instants naming one occurrence, and bare dates naming a local day.
 *
 * Exceptions and overridden starts are both read through here, so a start
 * written in either list matches the same candidate.
 */
function buildRecurrenceExceptionSets(
  values: string[] | undefined,
  zone: Zone,
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

    // An exception with no offset names a wall clock in the event's own
    // timezone — that is the reading the API validator documents and the
    // server expander applies. Resolved in the system zone instead, the
    // instant misses the occurrence it was written to cancel by exactly
    // the offset between the viewer and the event, so a cancelled meeting
    // reappeared for everyone whose browser sat in a different zone.
    const parsed = hasExplicitOffset(value)
      ? DateTime.fromISO(value, { setZone: true })
      : parseInZone(value, zone);
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

  // The one place the event's stored timezone becomes a resolved zone.
  // `calendar_events.timezone` is NOT NULL DEFAULT 'UTC' and resolves
  // event > user > workspace > UTC on the server, so a row that reaches
  // a client without one is a row whose zone is UTC — not a row that
  // wants the reader's. The Go expander
  // (packages/go-shared/recurrence) has always read an absent zone as
  // UTC; testdata/recurrence_golden.json pins both to it.
  const zone = Zone.resolve(event.timezone);
  const eventStart = parseInZone(event.startAt, zone);
  const eventEnd = parseInZone(event.endAt, zone);
  const duration = eventEnd.diff(eventStart);
  const interval = rule.interval ?? 1;
  // Two independent upper bounds, and the earlier one ends the series.
  const ruleUntil = rule.until ? parseUntil(rule.until, zone) : null;
  const seriesEnd = event.recurrenceEnd ? parseInZone(event.recurrenceEnd, zone) : null;
  const until =
    ruleUntil && seriesEnd
      ? ruleUntil < seriesEnd
        ? ruleUntil
        : seriesEnd
      : (ruleUntil ?? (seriesEnd?.isValid ? seriesEnd : null));
  const maxCount = rule.count ?? Number.POSITIVE_INFINITY;
  const exceptions = buildRecurrenceExceptionSets(event.recurrenceExceptions, zone);
  const overridden = buildRecurrenceExceptionSets(event.overriddenStarts, zone);

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
      // A cancelled occurrence still counts against COUNT: the series is
      // "ten meetings", and cancelling one does not conjure an eleventh at
      // the end. A replaced one counts for the stronger reason that it
      // still happens — it was moved, not cancelled — so it consumes a
      // count exactly as an ordinary occurrence does, and the ten meetings
      // are still ten.
      emitted++;
      const excluded =
        exceptions.instants.has(candidate.toMillis()) ||
        exceptions.localDayKeys.has(candidate.toFormat('yyyy-MM-dd'));
      // Suppressed by an override row, which renders this occurrence at its
      // own time. A separate check from the exception one, so which of the
      // two applies stays readable here.
      const replaced =
        overridden.instants.has(candidate.toMillis()) ||
        overridden.localDayKeys.has(candidate.toFormat('yyyy-MM-dd'));
      if (!excluded && !replaced) {
        const instanceEnd = candidate.plus(duration);
        if (instanceEnd > rangeStart) {
          results.push({ startAt: candidate, endAt: instanceEnd });
        }
      }
    }
  }

  return results;
}

/**
 * Expand all recurring events in a list, merging with non-recurring events.
 *
 * An override row has no `recurrenceRule` of its own, so it passes through
 * as an ordinary event and renders at its moved time. That is the half of
 * the model this function is responsible for; the master suppressing the
 * occurrence it replaced needs `overriddenStarts` on the master, which the
 * caller carries in alongside it.
 */
export function expandAllRecurrences<
  T extends {
    startAt: string;
    endAt: string;
    timezone?: string;
    recurrenceRule?: RecurrenceRule | null;
    recurrenceExceptions?: string[];
    overriddenStarts?: string[];
    recurrenceEnd?: string;
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
        overriddenStarts: event.overriddenStarts,
        recurrenceEnd: event.recurrenceEnd,
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
