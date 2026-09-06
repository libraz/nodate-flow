/**
 * What one calendar view asks the server for.
 *
 * The desktop grid draws a single month and moves between months by
 * button, so the window it fetches is that month's grid. The mobile view
 * scrolls freely through a year either side of today and moves the
 * cursor as it goes — a row reaches the top long before a fetch keyed to
 * it could return — so it asks for a month on each side in advance.
 *
 * The four windows here are one window in four shapes: the task query's
 * day keys, the event query's instants, the recurrence expander's
 * bounds, and the holiday provider's local dates. A buffer that reaches
 * only some of them loads events the expander then drops, or leaves the
 * extra months' holidays unmarked — both of which look exactly like the
 * empty rows the buffer exists to prevent.
 */

import { Zone } from '@nodate-flow/ui/time';
import { describe, expect, it, vi } from 'vitest';

// The route module calls `createFileRoute` at import time. Nothing here
// renders, so the returned value only has to be an object.
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => ({ options }),
  useNavigate: () => vi.fn(),
  Link: () => null,
}));

import { dateKey, endOfDayIso, startOfDayIso } from '../../lib/date-utils';
import { buildCalendarRange } from '../_authenticated.calendar';

/** September 2026, as the route's cursor holds it (month is 0-based). */
const CURSOR = { year: 2026, month: 8 };
const ZONE = Zone.utc();

const base = buildCalendarRange(CURSOR, 'mon', ZONE, 0);
const buffered = buildCalendarRange(CURSOR, 'mon', ZONE, 1);

/** True when `dayKey` falls inside the range's task window. */
function covers(range: { fromDate: string; toDate: string }, dayKey: string): boolean {
  return range.fromDate <= dayKey && dayKey <= range.toDate;
}

describe('buildCalendarRange', () => {
  it('asks for exactly the drawn month grid without a buffer', () => {
    // The 42-cell grid for September 2026 with a Monday start: it opens
    // on the Monday before the 1st and runs into the following month.
    expect(base.fromDate).toBe('2026-08-31');
    expect(base.toDate).toBe('2026-10-04');
  });

  it('reaches a month further on each side with a buffer of one', () => {
    expect(buffered.fromDate).toBe('2026-07-27');
    expect(buffered.toDate).toBe('2026-11-01');
    // Stated as a relation too, so a fixture whose two spans happened to
    // coincide could not pass this file.
    expect(buffered.fromDate < base.fromDate).toBe(true);
    expect(buffered.toDate > base.toDate).toBe(true);
  });

  it('covers the neighbouring months the unbuffered window leaves out', () => {
    // Days the mobile view scrolls to within one flick of September, and
    // which the drawn month's own grid never reaches.
    for (const day of ['2026-08-01', '2026-08-15', '2026-10-15', '2026-10-31']) {
      expect(covers(base, day)).toBe(false);
      expect(covers(buffered, day)).toBe(true);
    }
  });

  it('widens the event window with the task window', () => {
    expect(buffered.fromIso).toBe(startOfDayIso(buffered.fromDate, ZONE));
    expect(buffered.toIso).toBe(endOfDayIso(buffered.toDate, ZONE));
    expect(Date.parse(buffered.fromIso)).toBeLessThan(Date.parse(base.fromIso));
    expect(Date.parse(buffered.toIso)).toBeGreaterThan(Date.parse(base.toIso));
  });

  it('widens the recurrence expansion window with the event window', () => {
    // Occurrences are kept only if they fall inside these bounds, so a
    // fetch that outran them would discard the extra months it loaded.
    expect(buffered.rangeStart.toISOString()).toBe(new Date(buffered.fromIso).toISOString());
    expect(buffered.rangeEnd.toISOString()).toBe(new Date(buffered.toIso).toISOString());
    expect(buffered.rangeStart.getTime()).toBeLessThan(base.rangeStart.getTime());
    expect(buffered.rangeEnd.getTime()).toBeGreaterThan(base.rangeEnd.getTime());
  });

  it('widens the holiday window with the rest', () => {
    expect(dateKey(buffered.holidayFrom)).toBe(buffered.fromDate);
    expect(dateKey(buffered.holidayTo)).toBe(buffered.toDate);
    expect(buffered.holidayFrom.getTime()).toBeLessThan(base.holidayFrom.getTime());
    expect(buffered.holidayTo.getTime()).toBeGreaterThan(base.holidayTo.getTime());
  });

  it('crosses a year boundary in either direction', () => {
    const january = buildCalendarRange({ year: 2026, month: 0 }, 'mon', ZONE, 1);
    expect(january.fromDate.startsWith('2025-')).toBe(true);
    const december = buildCalendarRange({ year: 2026, month: 11 }, 'mon', ZONE, 1);
    expect(december.toDate.startsWith('2027-')).toBe(true);
  });
});
