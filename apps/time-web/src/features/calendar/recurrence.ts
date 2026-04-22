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

function advanceByFreq(dt: DateTime, freq: RecurrenceRule['freq'], interval: number): DateTime {
  switch (freq) {
    case 'daily':
      return dt.plus({ days: interval });
    case 'weekly':
      return dt.plus({ weeks: interval });
    case 'monthly':
      return dt.plus({ months: interval });
    case 'yearly':
      return dt.plus({ years: interval });
  }
}

function matchesByDay(dt: DateTime, byDay: string[]): boolean {
  const isoWeekday = dt.weekday;
  return byDay.some((d) => DAY_MAP[d.toLowerCase()] === isoWeekday);
}

function matchesByMonthDay(dt: DateTime, byMonthDay: number[]): boolean {
  return byMonthDay.includes(dt.day);
}

/**
 * Parse an ISO timestamp and anchor it to `zone` if provided, so subsequent
 * calendar arithmetic preserves wall-clock time across DST transitions.
 * Returns an invalid DateTime if parsing fails.
 */
function parseInZone(iso: string, zone: string | undefined): DateTime {
  return zone ? DateTime.fromISO(iso, { zone }) : DateTime.fromISO(iso);
}

/** Expand a recurring event into concrete instances within [rangeStart, rangeEnd). */
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
  const until = rule.until ? parseInZone(rule.until, zone) : null;
  const maxCount = rule.count ?? Number.POSITIVE_INFINITY;

  // Exceptions compare instants (UTC ms), so zone doesn't matter for the key.
  const exceptions = new Set(
    event.recurrenceExceptions?.map((d) => DateTime.fromISO(d).toMillis()) ?? [],
  );

  const results: ExpandedInstance[] = [];
  let current = eventStart;
  let emitted = 0;

  while (current < rangeEnd && emitted < maxCount) {
    if (until && current > until) break;

    const candidate = current;
    const passesDay = !rule.byDay || matchesByDay(candidate, rule.byDay);
    const passesMonthDay = !rule.byMonthDay || matchesByMonthDay(candidate, rule.byMonthDay);

    if (passesDay && passesMonthDay) {
      emitted++;
      if (!exceptions.has(candidate.toMillis())) {
        const instanceEnd = candidate.plus(duration);
        if (instanceEnd > rangeStart) {
          results.push({ startAt: candidate, endAt: instanceEnd });
        }
      }
    }

    current = advanceByFreq(current, rule.freq, interval);
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
