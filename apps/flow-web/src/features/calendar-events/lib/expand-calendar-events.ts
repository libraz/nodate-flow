import type { components } from '@nodate-flow/sdk';
import { expandAllRecurrences, type RecurrenceRule } from '@nodate-flow/ui/calendar';
import { Zone } from '@nodate-flow/ui/time';
import { DateTime } from 'luxon';

export type CalendarEvent = components['schemas']['MyCalendarEventResponse'];
type RecurrenceExpansionInput<T> = {
  event: T;
  startAt: string;
  endAt: string;
  timezone: string;
  recurrenceRule: RecurrenceRule | null;
  recurrenceExceptions?: string[];
  overriddenStarts?: string[];
  recurrenceEnd?: string;
};

/**
 * True when an event carries a recurrence rule.
 *
 * Dropping such a pill on another day raises the same "this occurrence /
 * this and following / the whole series" question the edit dialog asks
 * before it saves, so a person meets one question however they move an
 * event. Until a per-occurrence write existed these pills declined the
 * drag outright, because a master shifted in place silently rewrites
 * every occurrence it produces.
 */
export function isRecurring(event: CalendarEvent): boolean {
  const rule = event.recurrenceRule;
  if (rule == null) return false;
  if (typeof rule === 'string') return rule.trim().length > 0;
  // Non-string recurrence payloads (object form) count as recurring.
  return true;
}

function recurrenceRule(value: unknown): RecurrenceRule | null {
  if (!value || typeof value !== 'object') return null;
  const freq = (value as { freq?: unknown }).freq;
  if (freq !== 'daily' && freq !== 'weekly' && freq !== 'monthly' && freq !== 'yearly') return null;
  return value as RecurrenceRule;
}

/**
 * Read a list of occurrence starts off an event row.
 *
 * `recurrenceExceptions` and `overriddenStarts` are the same wire shape —
 * an array of RFC 3339 instants or bare `YYYY-MM-DD` days — and the
 * expander reads both through one parser, so they are narrowed here by
 * one function too. `recurrenceExceptions` is typed `unknown` by the
 * generated SDK, hence the runtime check rather than a cast.
 */
function occurrenceStarts(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const strings = value.filter((v): v is string => typeof v === 'string');
  return strings.length > 0 ? strings : undefined;
}

function secondsToIso(seconds: number): string {
  return new Date(seconds * 1000).toISOString();
}

function isoToSeconds(iso: string): number | undefined {
  const millis = Date.parse(iso);
  if (Number.isNaN(millis)) return undefined;
  return Math.floor(millis / 1000);
}

/**
 * Turn the stored rows from `/me/calendar-events` into the occurrences the
 * grid draws: recurring masters expand into instances, everything else —
 * including an override row, which carries no rule of its own — passes
 * through unchanged.
 */
export function expandCalendarEvents(
  events: CalendarEvent[],
  rangeStart: Date,
  rangeEnd: Date,
): CalendarEvent[] {
  const recurrenceInput: RecurrenceExpansionInput<CalendarEvent>[] = events.map((event) => {
    const rule = recurrenceRule(event.recurrenceRule);
    const exceptions = occurrenceStarts(event.recurrenceExceptions);
    const overridden = occurrenceStarts(event.overriddenStarts);
    const input: RecurrenceExpansionInput<CalendarEvent> = {
      event,
      startAt: typeof event.startAt === 'number' ? secondsToIso(event.startAt) : '',
      endAt:
        typeof event.endAt === 'number'
          ? secondsToIso(event.endAt)
          : typeof event.startAt === 'number'
            ? secondsToIso(event.startAt)
            : '',
      timezone: event.timezone,
      recurrenceRule: rule,
    };
    if (exceptions) input.recurrenceExceptions = exceptions;
    // The occurrences an override row already stands in for. Dropped here
    // the master keeps emitting the original occurrence while the override
    // row draws its moved copy, so one occurrence appears twice.
    if (overridden) input.overriddenStarts = overridden;
    // recurrence_end is the second upper bound on a series; without it a
    // series the API was told to stop keeps being drawn.
    if (typeof event.recurrenceEnd === 'number') {
      input.recurrenceEnd = secondsToIso(event.recurrenceEnd);
    }
    return input;
  });

  // The window bounds are instants and the expander only ever compares
  // them as instants, so the zone they are read in cannot change which
  // occurrences fall inside. Naming UTC rather than letting them adopt
  // the host zone keeps that true by construction instead of by
  // coincidence: the moment a bound is used for anything day-shaped,
  // an unnamed zone would quietly make the window the reader's.
  const utc = Zone.utc().name;
  return expandAllRecurrences(
    recurrenceInput,
    DateTime.fromJSDate(rangeStart, { zone: utc }),
    DateTime.fromJSDate(rangeEnd, { zone: utc }).plus({ milliseconds: 1 }),
  ).map((instance) => ({
    ...instance.event,
    ...((isoToSeconds(instance.startAt) ?? instance.event.startAt)
      ? { startAt: isoToSeconds(instance.startAt) ?? instance.event.startAt }
      : {}),
    ...((isoToSeconds(instance.endAt) ?? instance.event.endAt)
      ? { endAt: isoToSeconds(instance.endAt) ?? instance.event.endAt }
      : {}),
  }));
}
