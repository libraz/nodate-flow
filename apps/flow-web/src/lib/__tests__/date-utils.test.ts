import { describe, expect, it } from 'vitest';

import { dateKey, eventDateKey, eventStartOfDay, todayKey } from '../date-utils';

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
    expect(eventDateKey(allDayInstant, true)).toBe('2027-08-05');
  });

  it('reads a timed event in local time', () => {
    const timed = new Date(2027, 7, 5, 14, 30, 0, 0).getTime() / 1000;
    expect(eventDateKey(timed, false)).toBe('2027-08-05');
  });

  // The two cases below straddle UTC midnight from either side, so one
  // of them discriminates whatever offset the runner sits at. Asserting
  // only on midnight itself passes for the wrong reason east of
  // Greenwich, where the local read happens to agree — the same shape as
  // an hour-range check that cannot see a shifted window.
  it('does not shift an all-day event late in the UTC day', () => {
    // 23:00Z on the 4th is already the 5th at any positive offset.
    const lateEvening = Date.UTC(2027, 7, 4, 23, 0, 0, 0) / 1000;
    expect(eventDateKey(lateEvening, true)).toBe('2027-08-04');
  });

  it('does not shift an all-day event early in the UTC day', () => {
    // 01:00Z on the 5th is still the 4th at any offset below -01:00.
    const earlyMorning = Date.UTC(2027, 7, 5, 1, 0, 0, 0) / 1000;
    expect(eventDateKey(earlyMorning, true)).toBe('2027-08-05');
  });
});

describe('eventStartOfDay', () => {
  it('returns a local-midnight Date for the day the event belongs to', () => {
    const allDayInstant = Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000;
    const d = eventStartOfDay(allDayInstant, true);
    expect(d.getFullYear()).toBe(2027);
    expect(d.getMonth()).toBe(7);
    expect(d.getDate()).toBe(5);
    expect(d.getHours()).toBe(0);
  });

  it('agrees with eventDateKey for timed events', () => {
    const timed = new Date(2027, 7, 5, 23, 45, 0, 0).getTime() / 1000;
    const d = eventStartOfDay(timed, false);
    expect(dateKey(d)).toBe(eventDateKey(timed, false));
  });
});

describe('eventDateKey with an effective timezone', () => {
  // 2027-08-05T22:00Z is already the 6th in Tokyo and still the 5th in
  // New York. Which day the event belongs to is exactly the question the
  // profile timezone answers, and reading it in the browser's zone is
  // what made the setting a no-op.
  const timed = Date.UTC(2027, 7, 5, 22, 0, 0, 0) / 1000;

  it('files a timed event by the day it falls on in that zone', () => {
    expect(eventDateKey(timed, false, 'Asia/Tokyo')).toBe('2027-08-06');
    expect(eventDateKey(timed, false, 'America/New_York')).toBe('2027-08-05');
  });

  it('leaves all-day events in UTC whatever zone is supplied', () => {
    const allDay = Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000;
    expect(eventDateKey(allDay, true, 'Asia/Tokyo')).toBe('2027-08-05');
    expect(eventDateKey(allDay, true, 'America/New_York')).toBe('2027-08-05');
  });
});

describe('todayKey', () => {
  it('reads today in the supplied zone', () => {
    const noonUTC = new Date(Date.UTC(2027, 7, 5, 22, 0, 0, 0));
    expect(todayKey('Asia/Tokyo', noonUTC)).toBe('2027-08-06');
    expect(todayKey('America/New_York', noonUTC)).toBe('2027-08-05');
  });
});
