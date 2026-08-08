/**
 * Unit coverage for the public share's month-grid layout math (a port of
 * the authenticated calendar's week-layout). Verifies six-week grids,
 * zoned day bucketing, weekday ordering, and multi-day track packing.
 */

import type { components } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

import {
  buildMonthGrid,
  eventStartKey,
  expandShareEventsForMonth,
  isMultiDay,
  shiftMonthAnchor,
} from '../lib/share-month-grid';

type ShareEvent = components['schemas']['PublicShareRenderEvent'];

/** Unix seconds for a UTC wall-clock date/time. */
function utc(y: number, m: number, d: number, h = 0): number {
  return Math.floor(Date.UTC(y, m - 1, d, h) / 1000);
}

function evt(over: Partial<ShareEvent> & { id: string }): ShareEvent {
  return {
    allDay: false,
    flexibility: 'fixed',
    kind: 'event',
    showAs: 'busy',
    timezone: 'UTC',
    title: over.id,
    ...over,
  };
}

describe('buildMonthGrid', () => {
  it('renders six week rows of seven cells aligned to the week start', () => {
    const grid = buildMonthGrid('2024-03-01', [], 'UTC', 'sun');
    expect(grid.weeks).toHaveLength(6);
    for (const w of grid.weeks) expect(w.cells).toHaveLength(7);
    // March 2024: the 1st is a Friday; a Sunday-start grid leads with Feb 25.
    expect(grid.weeks[0]?.cells[0]?.key).toBe('2024-02-25');
    expect(grid.weekdayOrder[0]).toBe(0);
  });

  it('honours a Monday week start', () => {
    const grid = buildMonthGrid('2024-03-01', [], 'UTC', 'mon');
    expect(grid.weekdayOrder[0]).toBe(1);
    // Monday-start grid for March 2024 leads with Feb 26 (Mon).
    expect(grid.weeks[0]?.cells[0]?.key).toBe('2024-02-26');
  });

  it('marks in-month vs outside cells', () => {
    const grid = buildMonthGrid('2024-03-01', [], 'UTC', 'sun');
    const lead = grid.weeks[0]?.cells[0];
    expect(lead?.inMonth).toBe(false);
    const mid = grid.weeks[2]?.cells[0];
    expect(mid?.inMonth).toBe(true);
  });

  it('packs a multi-day event into a single spanning bar', () => {
    const e = evt({ id: 'm1', startAt: utc(2024, 3, 5), endAt: utc(2024, 3, 7) });
    expect(isMultiDay(e, 'UTC')).toBe(true);
    const grid = buildMonthGrid('2024-03-01', [e], 'UTC', 'sun');
    const bars = grid.weeks.flatMap((w) => w.bars);
    expect(bars).toHaveLength(1);
    expect(bars[0]?.span).toBe(3);
    expect(bars[0]?.continuesLeft).toBe(false);
    expect(bars[0]?.continuesRight).toBe(false);
  });

  it('clips a bar that crosses a week boundary into two segments', () => {
    // Sat 2024-03-09 -> Mon 2024-03-11 crosses the Sun-start week edge.
    const e = evt({ id: 'm2', startAt: utc(2024, 3, 9), endAt: utc(2024, 3, 11) });
    const grid = buildMonthGrid('2024-03-01', [e], 'UTC', 'sun');
    const bars = grid.weeks.flatMap((w) => w.bars);
    expect(bars).toHaveLength(2);
    expect(bars.some((b) => b.continuesRight)).toBe(true);
    expect(bars.some((b) => b.continuesLeft)).toBe(true);
  });

  it('keeps single-day events out of the bar overlay', () => {
    const e = evt({ id: 's1', startAt: utc(2024, 3, 6, 9), endAt: utc(2024, 3, 6, 10) });
    expect(isMultiDay(e, 'UTC')).toBe(false);
    const grid = buildMonthGrid('2024-03-01', [e], 'UTC', 'sun');
    expect(grid.weeks.flatMap((w) => w.bars)).toHaveLength(0);
    expect(eventStartKey(e, 'UTC')).toBe('2024-03-06');
  });

  it('buckets events by the share timezone, not the visitor zone', () => {
    // 2024-03-06 23:30 UTC is 2024-03-07 08:30 in Tokyo.
    const e = evt({ id: 'tz', startAt: utc(2024, 3, 6, 23), endAt: utc(2024, 3, 6, 23) });
    expect(eventStartKey(e, 'UTC')).toBe('2024-03-06');
    expect(eventStartKey(e, 'Asia/Tokyo')).toBe('2024-03-07');
  });

  it('expands recurring masters before month-grid layout', () => {
    const e = evt({
      id: 'r1',
      startAt: utc(2024, 3, 5, 9),
      endAt: utc(2024, 3, 5, 10),
      recurrenceRule: { freq: 'daily', interval: 1, count: 2 },
    });

    const expanded = expandShareEventsForMonth('2024-03-01', [e], 'UTC', 'sun');

    expect(expanded.map((event) => eventStartKey(event, 'UTC'))).toEqual([
      '2024-03-05',
      '2024-03-06',
    ]);
  });

  // The share render used to hand the rule over as a string holding its
  // own JSON, while every authenticated surface sent the object. The
  // server now sends the object here too; the parser keeps accepting the
  // string so a page served by an older build still expands.
  it('still expands a rule that arrives as a JSON string', () => {
    const e = evt({
      id: 'r2',
      startAt: utc(2024, 3, 5, 9),
      endAt: utc(2024, 3, 5, 10),
      recurrenceRule: JSON.stringify({ freq: 'daily', interval: 1, count: 2 }),
    });

    const expanded = expandShareEventsForMonth('2024-03-01', [e], 'UTC', 'sun');

    expect(expanded.map((event) => eventStartKey(event, 'UTC'))).toEqual([
      '2024-03-05',
      '2024-03-06',
    ]);
  });
});

describe('shiftMonthAnchor', () => {
  it('steps months and clamps to the 1st', () => {
    expect(shiftMonthAnchor('2024-03-15', 1)).toBe('2024-04-01');
    expect(shiftMonthAnchor('2024-01-31', -1)).toBe('2023-12-01');
    expect(shiftMonthAnchor('2024-12-01', 1)).toBe('2025-01-01');
  });
});
