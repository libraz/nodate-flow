import { DateTime } from 'luxon';
import { describe, expect, it } from 'vitest';

import { shiftEventDays } from '../lib/shift-event';

/**
 * A helper that reads the shifted result back in the event's own zone,
 * because that is where the invariant lives: the wall clock has to be
 * unchanged. Comparing unix seconds would just restate the arithmetic.
 */
function wallClock(seconds: number, zone: string): string {
  return DateTime.fromSeconds(seconds, { zone }).toFormat('yyyy-MM-dd HH:mm');
}

/**
 * Shift and assert the event was datable, so the cases below can read
 * the result without a non-null assertion at every use.
 */
function shiftOrFail(
  event: Parameters<typeof shiftEventDays>[0],
  dayDelta: number,
): { startAt: number; endAt: number } {
  const shifted = shiftEventDays(event, dayDelta);
  if (!shifted) throw new Error('expected a datable event');
  return shifted;
}

describe('shiftEventDays', () => {
  it('moves an event by whole days and keeps its time of day', () => {
    const start = DateTime.fromISO('2027-06-01T10:00', { zone: 'UTC' }).toSeconds();
    const shifted = shiftOrFail({ startAt: start, endAt: start + 3600, timezone: 'UTC' }, 3);
    expect(wallClock(shifted.startAt, 'UTC')).toBe('2027-06-04 10:00');
    expect(wallClock(shifted.endAt, 'UTC')).toBe('2027-06-04 11:00');
  });

  // The case the fix exists for. 2027-03-14 is the US spring transition,
  // so the span from the 13th to the 15th is 47 hours, not 48. Adding
  // 2 * 86_400 seconds lands at 11:00 and saves that for everyone on the
  // invitation.
  it('keeps the wall clock across a spring-forward transition', () => {
    const zone = 'America/New_York';
    const start = DateTime.fromISO('2027-03-13T10:00', { zone }).toSeconds();
    const shifted = shiftOrFail({ startAt: start, endAt: start + 3600, timezone: zone }, 2);
    expect(wallClock(shifted.startAt, zone)).toBe('2027-03-15 10:00');
    expect(wallClock(shifted.endAt, zone)).toBe('2027-03-15 11:00');

    // And the naive arithmetic really would have been wrong here, so the
    // assertion above is not passing by coincidence.
    expect(wallClock(start + 2 * 86_400, zone)).toBe('2027-03-15 11:00');
  });

  it('keeps the wall clock across a fall-back transition', () => {
    const zone = 'America/New_York';
    const start = DateTime.fromISO('2027-11-06T10:00', { zone }).toSeconds();
    const shifted = shiftOrFail({ startAt: start, endAt: start + 3600, timezone: zone }, 2);
    expect(wallClock(shifted.startAt, zone)).toBe('2027-11-08 10:00');
    expect(wallClock(start + 2 * 86_400, zone)).toBe('2027-11-08 09:00');
  });

  // The event's zone decides, not the viewer's. A Tokyo meeting dragged
  // by a colleague elsewhere must keep its Tokyo time; JST has no DST,
  // so a shift across a US transition must not move it at all.
  it("uses the event timezone rather than the caller's", () => {
    const start = DateTime.fromISO('2027-03-13T10:00', { zone: 'Asia/Tokyo' }).toSeconds();
    const shifted = shiftOrFail({ startAt: start, endAt: start, timezone: 'Asia/Tokyo' }, 2);
    expect(wallClock(shifted.startAt, 'Asia/Tokyo')).toBe('2027-03-15 10:00');
  });

  it('falls back to UTC when the event carries no timezone', () => {
    const start = DateTime.fromISO('2027-06-01T10:00', { zone: 'UTC' }).toSeconds();
    const shifted = shiftOrFail({ startAt: start, endAt: start }, 1);
    expect(wallClock(shifted.startAt, 'UTC')).toBe('2027-06-02 10:00');
  });

  it('returns null for an undated event', () => {
    expect(shiftEventDays({ timezone: 'UTC' }, 1)).toBeNull();
  });

  it('treats a missing end as equal to the start', () => {
    const start = DateTime.fromISO('2027-06-01T10:00', { zone: 'UTC' }).toSeconds();
    const shifted = shiftOrFail({ startAt: start, timezone: 'UTC' }, 1);
    expect(shifted.endAt).toBe(shifted.startAt);
  });
});
