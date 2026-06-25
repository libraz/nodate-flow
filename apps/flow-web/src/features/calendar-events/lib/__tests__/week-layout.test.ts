/**
 * Unit tests for the mobile month-scroll week-layout math.
 *
 * The layout packs multi-day events into non-overlapping horizontal
 * tracks per Sunday/Monday-aligned week and reports per-bar clipping at
 * the week edges. These tests pin the span / track / clip arithmetic so
 * a regression to the column math fails loudly without rendering.
 */

import type { components } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

import { isMultiDay, layoutWeek } from '../week-layout';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

/** Local-midnight unix seconds for a given Y-M-D (month is 1-based). */
function unix(y: number, m: number, d: number, hour = 9): number {
  return Math.floor(new Date(y, m - 1, d, hour, 0, 0, 0).getTime() / 1000);
}

function makeEvent(partial: Partial<CalendarEvent> & { id: string }): CalendarEvent {
  return {
    allDay: false,
    attendeeCount: 0,
    calendarId: 'cal-1',
    createdAt: 0,
    kind: 'event',
    ownerUserId: 'u1',
    showAs: 'busy',
    timezone: 'UTC',
    title: partial.id,
    viewerAttending: false,
    visibility: 'default',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
    ...partial,
  };
}

describe('isMultiDay', () => {
  it('returns false for a same-day event', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 6, 3, 9), endAt: unix(2026, 6, 3, 11) });
    expect(isMultiDay(evt)).toBe(false);
  });

  it('returns true when the event crosses a day boundary', () => {
    const evt = makeEvent({ id: 'b', startAt: unix(2026, 6, 3, 9), endAt: unix(2026, 6, 5, 11) });
    expect(isMultiDay(evt)).toBe(true);
  });

  it('ignores single-day events with no explicit end', () => {
    const evt = makeEvent({ id: 'c', startAt: unix(2026, 6, 3, 9) });
    expect(isMultiDay(evt)).toBe(false);
  });
});

describe('layoutWeek', () => {
  // Sunday 2026-05-31 .. Saturday 2026-06-06.
  const weekStart = new Date(2026, 4, 31, 0, 0, 0, 0);

  it('positions a fully-contained multi-day bar with correct span', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 6, 1, 9), endAt: unix(2026, 6, 3, 11) });
    const [pos] = layoutWeek(weekStart, [evt]);
    expect(pos).toBeDefined();
    expect(pos?.startCol).toBe(1); // Monday
    expect(pos?.span).toBe(3); // Mon..Wed inclusive
    expect(pos?.track).toBe(0);
    expect(pos?.continuesLeft).toBe(false);
    expect(pos?.continuesRight).toBe(false);
  });

  it('clips a bar that starts before the week', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 5, 28, 9), endAt: unix(2026, 6, 2, 11) });
    const [pos] = layoutWeek(weekStart, [evt]);
    expect(pos?.startCol).toBe(0);
    expect(pos?.continuesLeft).toBe(true);
    expect(pos?.continuesRight).toBe(false);
    expect(pos?.span).toBe(3); // Sun..Tue visible
  });

  it('clips a bar that ends after the week', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 6, 5, 9), endAt: unix(2026, 6, 10, 11) });
    const [pos] = layoutWeek(weekStart, [evt]);
    expect(pos?.startCol).toBe(5); // Friday
    expect(pos?.span).toBe(2); // Fri..Sat visible
    expect(pos?.continuesRight).toBe(true);
  });

  it('stacks overlapping bars onto separate tracks', () => {
    const a = makeEvent({ id: 'a', startAt: unix(2026, 6, 1, 9), endAt: unix(2026, 6, 3, 11) });
    const b = makeEvent({ id: 'b', startAt: unix(2026, 6, 2, 9), endAt: unix(2026, 6, 4, 11) });
    const positioned = layoutWeek(weekStart, [a, b]);
    const tracks = positioned.map((p) => p.track).sort();
    expect(tracks).toEqual([0, 1]);
  });

  it('reuses a track for non-overlapping bars', () => {
    const a = makeEvent({ id: 'a', startAt: unix(2026, 6, 1, 9), endAt: unix(2026, 6, 2, 11) });
    const b = makeEvent({ id: 'b', startAt: unix(2026, 6, 4, 9), endAt: unix(2026, 6, 5, 11) });
    const positioned = layoutWeek(weekStart, [a, b]);
    expect(positioned.every((p) => p.track === 0)).toBe(true);
  });

  it('excludes single-day events from the bar layout', () => {
    const single = makeEvent({
      id: 's',
      startAt: unix(2026, 6, 2, 9),
      endAt: unix(2026, 6, 2, 11),
    });
    expect(layoutWeek(weekStart, [single])).toHaveLength(0);
  });
});
