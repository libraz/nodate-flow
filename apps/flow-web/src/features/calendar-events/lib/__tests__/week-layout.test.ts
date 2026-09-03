/**
 * Unit tests for the mobile month-scroll week-layout math.
 *
 * The layout packs multi-day events into non-overlapping horizontal
 * tracks per Sunday/Monday-aligned week and reports per-bar clipping at
 * the week edges. These tests pin the span / track / clip arithmetic so
 * a regression to the column math fails loudly without rendering.
 */

import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { describe, expect, it } from 'vitest';

import {
  eventStartKey,
  groupEventsByWeek,
  isMultiDay,
  layoutWeek,
  startOfDay,
} from '../week-layout';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];

/**
 * The fixtures below are built from local wall clocks and compared
 * against week rows built the same way, so the host zone is the zone
 * that makes the round trip exact and leaves these assertions about the
 * geometry rather than about day boundaries. Which zone an event's day
 * is read in is covered where it is decided, not here.
 */
const hostZone = Zone.browser();

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
    flexibility: 'fixed',
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
    expect(isMultiDay(evt, hostZone)).toBe(false);
  });

  it('returns true when the event crosses a day boundary', () => {
    const evt = makeEvent({ id: 'b', startAt: unix(2026, 6, 3, 9), endAt: unix(2026, 6, 5, 11) });
    expect(isMultiDay(evt, hostZone)).toBe(true);
  });

  it('ignores single-day events with no explicit end', () => {
    const evt = makeEvent({ id: 'c', startAt: unix(2026, 6, 3, 9) });
    expect(isMultiDay(evt, hostZone)).toBe(false);
  });
});

describe('layoutWeek', () => {
  // Sunday 2026-05-31 .. Saturday 2026-06-06.
  const weekStart = new Date(2026, 4, 31, 0, 0, 0, 0);

  it('positions a fully-contained multi-day bar with correct span', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 6, 1, 9), endAt: unix(2026, 6, 3, 11) });
    const [pos] = layoutWeek(weekStart, [evt], hostZone);
    expect(pos).toBeDefined();
    expect(pos?.startCol).toBe(1); // Monday
    expect(pos?.span).toBe(3); // Mon..Wed inclusive
    expect(pos?.track).toBe(0);
    expect(pos?.continuesLeft).toBe(false);
    expect(pos?.continuesRight).toBe(false);
  });

  it('clips a bar that starts before the week', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 5, 28, 9), endAt: unix(2026, 6, 2, 11) });
    const [pos] = layoutWeek(weekStart, [evt], hostZone);
    expect(pos?.startCol).toBe(0);
    expect(pos?.continuesLeft).toBe(true);
    expect(pos?.continuesRight).toBe(false);
    expect(pos?.span).toBe(3); // Sun..Tue visible
  });

  it('clips a bar that ends after the week', () => {
    const evt = makeEvent({ id: 'a', startAt: unix(2026, 6, 5, 9), endAt: unix(2026, 6, 10, 11) });
    const [pos] = layoutWeek(weekStart, [evt], hostZone);
    expect(pos?.startCol).toBe(5); // Friday
    expect(pos?.span).toBe(2); // Fri..Sat visible
    expect(pos?.continuesRight).toBe(true);
  });

  it('stacks overlapping bars onto separate tracks', () => {
    const a = makeEvent({ id: 'a', startAt: unix(2026, 6, 1, 9), endAt: unix(2026, 6, 3, 11) });
    const b = makeEvent({ id: 'b', startAt: unix(2026, 6, 2, 9), endAt: unix(2026, 6, 4, 11) });
    const positioned = layoutWeek(weekStart, [a, b], hostZone);
    const tracks = positioned.map((p) => p.track).sort();
    expect(tracks).toEqual([0, 1]);
  });

  it('reuses a track for non-overlapping bars', () => {
    const a = makeEvent({ id: 'a', startAt: unix(2026, 6, 1, 9), endAt: unix(2026, 6, 2, 11) });
    const b = makeEvent({ id: 'b', startAt: unix(2026, 6, 4, 9), endAt: unix(2026, 6, 5, 11) });
    const positioned = layoutWeek(weekStart, [a, b], hostZone);
    expect(positioned.every((p) => p.track === 0)).toBe(true);
  });

  it('excludes single-day events from the bar layout', () => {
    const single = makeEvent({
      id: 's',
      startAt: unix(2026, 6, 2, 9),
      endAt: unix(2026, 6, 2, 11),
    });
    expect(layoutWeek(weekStart, [single], hostZone)).toHaveLength(0);
  });
});

describe('groupEventsByWeek', () => {
  /** Two years of Monday-aligned week rows, as the month view renders. */
  const weekStarts = Array.from({ length: 109 }, (_, i) =>
    startOfDay(new Date(2026, 0, 5 + i * 7)),
  );
  const key = (d: Date): string =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;

  /** A spread of single- and multi-day events across the whole range. */
  const events: CalendarEvent[] = Array.from({ length: 300 }, (_, i) => {
    const start = new Date(2026, 0, 5 + ((i * 3) % 730), 9);
    const spanDays = i % 7 === 0 ? 9 : i % 3 === 0 ? 1 : 0;
    const end = new Date(start.getTime() + spanDays * 86_400_000 + 3_600_000);
    return makeEvent({
      id: `e-${i}`,
      startAt: Math.floor(start.getTime() / 1000),
      endAt: Math.floor(end.getTime() / 1000),
    });
  });

  it('gives every week exactly the bars it got from the whole list', () => {
    // What the view did before: hand each row all the events and let it
    // filter. Grouping first must not change a single row's outcome.
    const grouped = groupEventsByWeek(events, weekStarts, key, hostZone);
    for (const ws of weekStarts) {
      const fromAll = layoutWeek(ws, events, hostZone);
      const fromBucket = layoutWeek(ws, grouped.get(key(ws)) ?? [], hostZone);
      expect(fromBucket, key(ws)).toEqual(fromAll);
    }
  });

  it('gives every week exactly the single-day events it showed before', () => {
    const grouped = groupEventsByWeek(events, weekStarts, key, hostZone);
    for (const ws of weekStarts) {
      const weekKeys = new Set(
        Array.from({ length: 7 }, (_, i) =>
          key(startOfDay(new Date(ws.getTime() + i * 86_400_000))),
        ),
      );
      const singlesIn = (list: CalendarEvent[]): string[] =>
        list
          .filter((e) => !isMultiDay(e, hostZone))
          .filter((e) => {
            const k = eventStartKey(e, hostZone);
            return k !== null && weekKeys.has(k);
          })
          .map((e) => e.id)
          .sort();
      expect(singlesIn(grouped.get(key(ws)) ?? []), key(ws)).toEqual(singlesIn(events));
    }
  });

  it('files a multi-day event under every week it crosses', () => {
    const spanning = makeEvent({
      id: 'long',
      startAt: Math.floor(new Date(2026, 0, 7, 9).getTime() / 1000),
      endAt: Math.floor(new Date(2026, 0, 20, 9).getTime() / 1000),
    });
    const grouped = groupEventsByWeek([spanning], weekStarts, key, hostZone);
    const weeksHolding = weekStarts.filter((ws) => grouped.get(key(ws))?.length);
    expect(weeksHolding.map(key)).toEqual(['2026-01-05', '2026-01-12', '2026-01-19']);
  });

  it('leaves weeks outside the range alone', () => {
    const before = makeEvent({
      id: 'before',
      startAt: Math.floor(new Date(2020, 0, 1, 9).getTime() / 1000),
      endAt: Math.floor(new Date(2020, 0, 1, 10).getTime() / 1000),
    });
    expect(groupEventsByWeek([before], weekStarts, key, hostZone).size).toBe(0);
  });
});
