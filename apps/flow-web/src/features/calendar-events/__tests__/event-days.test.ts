import { Zone } from '@nodate-flow/ui/time';
import { describe, expect, it } from 'vitest';

import { eventDayKeys, MAX_EVENT_SPAN_DAYS } from '../lib/event-days';

const utc = Zone.utc();

/**
 * Unix seconds for a wall-clock moment in the zone the assertions read
 * events in. Named absolutely rather than taken from the host, so the
 * expected keys below are the same on every machine — a helper that
 * built the instant in the runner's zone and then read it back in the
 * same zone would agree with itself no matter what the code did.
 */
function at(y: number, m: number, d: number, hh = 0, mm = 0): number {
  return Date.UTC(y, m - 1, d, hh, mm, 0, 0) / 1000;
}

/** Unix seconds for midnight UTC on a date, as the API stores all-day rows. */
function utcDate(y: number, m: number, d: number): number {
  return Date.UTC(y, m - 1, d, 0, 0, 0, 0) / 1000;
}

describe('eventDayKeys', () => {
  it('gives a single-day event one key', () => {
    expect(
      eventDayKeys(
        {
          startAt: at(2027, 6, 1, 10),
          endAt: at(2027, 6, 1, 11),
        } as never,
        utc,
      ),
    ).toEqual(['2027-06-01']);
  });

  // The case the desktop grid dropped: everything after the first day.
  it('gives a multi-day event one key per day it covers', () => {
    expect(
      eventDayKeys(
        {
          startAt: at(2027, 6, 1, 9),
          endAt: at(2027, 6, 5, 17),
        } as never,
        utc,
      ),
    ).toEqual(['2027-06-01', '2027-06-02', '2027-06-03', '2027-06-04', '2027-06-05']);
  });

  it('includes both ends of a two-day event', () => {
    expect(
      eventDayKeys(
        {
          startAt: at(2027, 6, 1, 22),
          endAt: at(2027, 6, 2, 1),
        } as never,
        utc,
      ),
    ).toEqual(['2027-06-01', '2027-06-02']);
  });

  it('spans a month boundary', () => {
    expect(
      eventDayKeys(
        {
          startAt: at(2027, 5, 30, 9),
          endAt: at(2027, 6, 2, 9),
        } as never,
        utc,
      ),
    ).toEqual(['2027-05-30', '2027-05-31', '2027-06-01', '2027-06-02']);
  });

  // All-day rows are stored at midnight UTC and have to name the same
  // squares for every viewer, so their days are read in UTC.
  it('reads an all-day span in UTC', () => {
    expect(
      eventDayKeys(
        {
          startAt: utcDate(2027, 8, 5),
          endAt: utcDate(2027, 8, 7),
          allDay: true,
        } as never,
        utc,
      ),
    ).toEqual(['2027-08-05', '2027-08-06', '2027-08-07']);
  });

  it('treats a missing end as a single day', () => {
    expect(eventDayKeys({ startAt: at(2027, 6, 1, 10) } as never, utc)).toEqual(['2027-06-01']);
  });

  it('returns nothing for an undated event', () => {
    expect(eventDayKeys({} as never, utc)).toEqual([]);
  });

  // An end before the start is malformed rather than negative-length;
  // it must yield the start day and stop, not loop.
  it('does not run backwards when the end precedes the start', () => {
    expect(
      eventDayKeys(
        {
          startAt: at(2027, 6, 5, 10),
          endAt: at(2027, 6, 1, 10),
        } as never,
        utc,
      ),
    ).toEqual(['2027-06-05']);
  });

  // A far-future end is a data problem, and without the cap it would
  // turn building one month's grid into a hang.
  it('caps a runaway span', () => {
    const keys = eventDayKeys(
      {
        startAt: at(2027, 6, 1),
        endAt: at(2099, 6, 1),
      } as never,
      utc,
    );
    expect(keys.length).toBe(MAX_EVENT_SPAN_DAYS + 1);
    expect(keys[0]).toBe('2027-06-01');
  });
});
