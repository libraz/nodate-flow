import { Zone } from '@nodate-flow/ui/time';
import { describe, expect, it } from 'vitest';

import fixtures from '../../../../../sql/core/all-day/fixtures.json';
import {
  allDayLastDateKey,
  allDayRangeToUnix,
  dateKey,
  endOfDayUnix,
  eventDateKey,
  eventStartOfDay,
  formatAllDayRange,
  todayKey,
} from '../date-utils';

const utc = Zone.utc();
const tokyo = Zone.resolve('Asia/Tokyo');
const newYork = Zone.resolve('America/New_York');

describe('dateKey', () => {
  it('formats a normal date as YYYY-MM-DD', () => {
    expect(dateKey(new Date(2024, 0, 15))).toBe('2024-01-15');
  });

  it('pads single-digit months with a leading zero', () => {
    expect(dateKey(new Date(2024, 2, 10))).toBe('2024-03-10');
  });

  it('pads single-digit days with a leading zero', () => {
    expect(dateKey(new Date(2024, 5, 5))).toBe('2024-06-05');
  });

  it('handles December 31 at the year boundary', () => {
    expect(dateKey(new Date(2024, 11, 31))).toBe('2024-12-31');
  });

  it('handles January 1 at the year boundary', () => {
    expect(dateKey(new Date(2025, 0, 1))).toBe('2025-01-01');
  });

  it('handles a leap year date (Feb 29)', () => {
    expect(dateKey(new Date(2024, 1, 29))).toBe('2024-02-29');
  });
});

describe('eventDateKey', () => {
  // An all-day event is a date, and a date is the same square on the
  // calendar for everyone. The API stores it at midnight UTC so there is
  // one answer; reading it with local getters makes it two again.
  const allDayInstant = Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000;

  it('reads an all-day event in UTC', () => {
    expect(eventDateKey(allDayInstant, true, utc)).toBe('2027-08-05');
  });

  it('reads a timed event in the zone it is given', () => {
    const timed = Date.UTC(2027, 7, 5, 14, 30, 0, 0) / 1000;
    expect(eventDateKey(timed, false, utc)).toBe('2027-08-05');
  });

  // The two cases below straddle UTC midnight from either side, so one
  // of them discriminates whatever offset the runner sits at. Asserting
  // only on midnight itself passes for the wrong reason east of
  // Greenwich, where the local read happens to agree — the same shape as
  // an hour-range check that cannot see a shifted window.
  it('does not shift an all-day event late in the UTC day', () => {
    // 23:00Z on the 4th is already the 5th at any positive offset.
    const lateEvening = Date.UTC(2027, 7, 4, 23, 0, 0, 0) / 1000;
    expect(eventDateKey(lateEvening, true, utc)).toBe('2027-08-04');
  });

  it('does not shift an all-day event early in the UTC day', () => {
    // 01:00Z on the 5th is still the 4th at any offset below -01:00.
    const earlyMorning = Date.UTC(2027, 7, 5, 1, 0, 0, 0) / 1000;
    expect(eventDateKey(earlyMorning, true, utc)).toBe('2027-08-05');
  });
});

describe('eventStartOfDay', () => {
  it('returns a local-midnight Date for the day the event belongs to', () => {
    const allDayInstant = Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000;
    const d = eventStartOfDay(allDayInstant, true, utc);
    expect(d.getFullYear()).toBe(2027);
    expect(d.getMonth()).toBe(7);
    expect(d.getDate()).toBe(5);
    expect(d.getHours()).toBe(0);
  });

  it('agrees with eventDateKey for timed events', () => {
    const timed = Date.UTC(2027, 7, 5, 23, 45, 0, 0) / 1000;
    const d = eventStartOfDay(timed, false, tokyo);
    expect(dateKey(d)).toBe(eventDateKey(timed, false, tokyo));
  });
});

describe('eventDateKey with an effective timezone', () => {
  // 2027-08-05T22:00Z is already the 6th in Tokyo and still the 5th in
  // New York. Which day the event belongs to is exactly the question the
  // profile timezone answers, and reading it in the browser's zone is
  // what made the setting a no-op.
  const timed = Date.UTC(2027, 7, 5, 22, 0, 0, 0) / 1000;

  it('files a timed event by the day it falls on in that zone', () => {
    expect(eventDateKey(timed, false, tokyo)).toBe('2027-08-06');
    expect(eventDateKey(timed, false, newYork)).toBe('2027-08-05');
  });

  it('leaves all-day events in UTC whatever zone is supplied', () => {
    const allDay = Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000;
    expect(eventDateKey(allDay, true, tokyo)).toBe('2027-08-05');
    expect(eventDateKey(allDay, true, newYork)).toBe('2027-08-05');
  });
});

describe('todayKey', () => {
  it('reads today in the supplied zone', () => {
    const noonUTC = new Date(Date.UTC(2027, 7, 5, 22, 0, 0, 0));
    expect(todayKey(tokyo, noonUTC)).toBe('2027-08-06');
    expect(todayKey(newYork, noonUTC)).toBe('2027-08-05');
  });
});

describe('endOfDayUnix', () => {
  it('ends the day at local midnight in a zone east of UTC', () => {
    expect(new Date((endOfDayUnix('2026-03-15', tokyo) + 1) * 1000).toISOString()).toBe(
      '2026-03-15T15:00:00.000Z',
    );
  });

  it('ends the day at local midnight in a zone west of UTC', () => {
    expect(new Date((endOfDayUnix('2026-03-15', newYork) + 1) * 1000).toISOString()).toBe(
      '2026-03-16T04:00:00.000Z',
    );
  });

  it('ends a UTC day one second before midnight UTC', () => {
    expect(endOfDayUnix('2026-03-15', utc)).toBe(Date.UTC(2026, 2, 16) / 1000 - 1);
  });
});

describe('all-day range conversion', () => {
  const secs = (iso: string): number => Date.parse(iso) / 1000;

  it('stores end as the day after the last day', () => {
    expect(allDayRangeToUnix('2026-03-10', '2026-03-10')).toEqual({
      startAt: secs('2026-03-10T00:00:00Z'),
      endAt: secs('2026-03-11T00:00:00Z'),
    });
  });

  it('stores a milestone as a point', () => {
    const r = allDayRangeToUnix('2026-03-10', '2026-03-10', true);
    expect(r.endAt).toBe(r.startAt);
  });

  it('displays the last day as end minus one day', () => {
    expect(allDayLastDateKey(secs('2026-03-10T00:00:00Z'), secs('2026-03-13T00:00:00Z'))).toBe(
      '2026-03-12',
    );
    expect(allDayLastDateKey(secs('2026-03-10T00:00:00Z'), secs('2026-03-10T00:00:00Z'))).toBe(
      '2026-03-10',
    );
  });

  describe('fixtures.json', () => {
    for (const c of fixtures.cases) {
      for (const [zoneName, v] of Object.entries(c.viewers)) {
        it(`${c.name} as seen from ${zoneName}`, () => {
          const startAt = secs(c.startAt);
          const endAt = secs(c.endAt);
          const zone = Zone.resolve(zoneName);
          const milestone = c.kind === 'milestone';
          expect(eventDateKey(startAt, true, zone)).toBe(v.firstDate);
          expect(allDayLastDateKey(startAt, endAt)).toBe(v.lastDate);
          expect(allDayRangeToUnix(v.firstDate, v.lastDate, milestone)).toEqual({ startAt, endAt });
          expect(allDayRangeToUnix(v.startDate, v.lastDate, milestone).endAt).toBe(endAt);
        });
      }
    }
  });
});

describe('formatAllDayRange', () => {
  const d = (n: number): number => Date.UTC(2026, 2, n) / 1000;

  it('shows a one-day event as one date plus the suffix', () => {
    expect(formatAllDayRange(d(10), d(11), 'en', ' · all day')).toBe('Mar 10, 2026 · all day');
  });

  it('ends a multi-day range on its last day', () => {
    expect(formatAllDayRange(d(10), d(13), 'en')).toBe('Mar 10, 2026 – Mar 12, 2026');
  });
});
