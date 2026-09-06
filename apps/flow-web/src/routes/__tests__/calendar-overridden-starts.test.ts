/**
 * A recurring occurrence that was edited on its own must be drawn once.
 *
 * Editing one occurrence with scope "this event" stores a separate override
 * row and records that occurrence's original start on the master. The
 * expander suppresses a recorded start, but only if the caller hands it
 * over: read off the API response and dropped on the floor, the master
 * still emits the occurrence the override row replaced and the day shows
 * two entries for one meeting.
 *
 * This covers the hop from the `/me/calendar-events` response into the
 * expander's input, which is the part that lives in this app; the
 * suppression itself is pinned in `packages/ui`.
 */

import type { components } from '@nodate-flow/sdk';
import { describe, expect, it, vi } from 'vitest';

// The route module calls `createFileRoute` at import time. Nothing here
// renders, so the returned value only has to be an object.
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => ({ options }),
  useNavigate: () => vi.fn(),
  Link: () => null,
}));

import { expandCalendarEvents } from '../_authenticated.calendar';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

/** Unix seconds for a UTC wall clock. */
function utc(iso: string): number {
  return Math.floor(Date.parse(iso) / 1000);
}

/**
 * The window the grid asks for when it is showing September 2026: the
 * six-week block from Monday the 31st of August, so it runs a week into
 * October the way the drawn grid does.
 */
const RANGE_START = new Date('2026-08-31T00:00:00Z');
const RANGE_END = new Date('2026-10-11T23:59:59Z');

/** Every Wednesday from the 9th, so five occurrences fall in the window. */
function weeklyMaster(overriddenStarts?: string[]): CalendarEvent {
  return {
    allDay: false,
    attendeeCount: 0,
    calendarId: 'cal-1',
    createdAt: 0,
    endAt: utc('2026-09-09T12:30:00Z'),
    flexibility: 'fixed',
    id: 'evt-master',
    kind: 'event',
    ownerUserId: 'u1',
    recurrenceRule: { freq: 'weekly', interval: 1 },
    showAs: 'busy',
    startAt: utc('2026-09-09T12:00:00Z'),
    timezone: 'UTC',
    title: 'Weekly standup',
    viewerAttending: false,
    visibility: 'default',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    ...(overriddenStarts ? { overriddenStarts } : {}),
  } as CalendarEvent;
}

/** The row the "this event" edit produced, moved later on the same day. */
function overrideRow(): CalendarEvent {
  return {
    ...weeklyMaster(),
    id: 'evt-override',
    title: 'Weekly standup (moved)',
    recurrenceRule: null,
    startAt: utc('2026-09-30T15:00:00Z'),
    endAt: utc('2026-09-30T15:30:00Z'),
  } as CalendarEvent;
}

/** The UTC `YYYY-MM-DD` of every expanded instance, in order. */
function daysOf(events: CalendarEvent[]): string[] {
  return events
    .map((e) => (typeof e.startAt === 'number' ? new Date(e.startAt * 1000) : null))
    .filter((d): d is Date => d !== null)
    .map((d) => d.toISOString().slice(0, 10))
    .sort();
}

describe('expandCalendarEvents — overridden starts', () => {
  it('draws an overridden occurrence once, from the override row', () => {
    const expanded = expandCalendarEvents(
      [weeklyMaster(['2026-09-30T12:00:00Z']), overrideRow()],
      RANGE_START,
      RANGE_END,
    );

    expect(daysOf(expanded)).toEqual([
      '2026-09-09',
      '2026-09-16',
      '2026-09-23',
      '2026-09-30',
      '2026-10-07',
    ]);

    const onThe30th = expanded.filter(
      (e) =>
        typeof e.startAt === 'number' &&
        new Date(e.startAt * 1000).toISOString().startsWith('2026-09-30'),
    );
    expect(onThe30th).toHaveLength(1);
    expect(onThe30th[0]?.id).toBe('evt-override');
  });

  it('leaves the other occurrences alone', () => {
    const expanded = expandCalendarEvents(
      [weeklyMaster(['2026-09-30T12:00:00Z'])],
      RANGE_START,
      RANGE_END,
    );

    expect(daysOf(expanded)).toEqual(['2026-09-09', '2026-09-16', '2026-09-23', '2026-10-07']);
  });

  it('accepts a bare local day as an overridden start', () => {
    const expanded = expandCalendarEvents([weeklyMaster(['2026-09-16'])], RANGE_START, RANGE_END);

    expect(daysOf(expanded)).toEqual(['2026-09-09', '2026-09-23', '2026-09-30', '2026-10-07']);
  });

  it('without the field the master still emits the replaced occurrence', () => {
    // The state this fix leaves behind, kept explicit so the assertions
    // above cannot pass for some reason other than the field arriving.
    const expanded = expandCalendarEvents([weeklyMaster(), overrideRow()], RANGE_START, RANGE_END);

    expect(daysOf(expanded)).toEqual([
      '2026-09-09',
      '2026-09-16',
      '2026-09-23',
      '2026-09-30',
      '2026-09-30',
      '2026-10-07',
    ]);
  });
});
