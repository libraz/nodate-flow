import { DateTime } from 'luxon';

import type { RecurrenceRule } from './types';

interface RecurrenceEvent {
  startAt: string;
  endAt: string;
  recurrenceRule: RecurrenceRule | null;
  recurrenceExceptions?: string[];
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

/** Expand a recurring event into concrete instances within [rangeStart, rangeEnd). */
export function expandRecurrence(
  event: RecurrenceEvent,
  rangeStart: DateTime,
  rangeEnd: DateTime,
): ExpandedInstance[] {
  const rule = event.recurrenceRule;
  if (!rule) return [];

  const eventStart = DateTime.fromISO(event.startAt);
  const eventEnd = DateTime.fromISO(event.endAt);
  const duration = eventEnd.diff(eventStart);
  const interval = rule.interval ?? 1;
  const until = rule.until ? DateTime.fromISO(rule.until) : null;
  const maxCount = rule.count ?? Number.POSITIVE_INFINITY;

  // Build a set of exception timestamps for fast lookup.
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
      // Skip instances that match a recurrence exception date.
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
