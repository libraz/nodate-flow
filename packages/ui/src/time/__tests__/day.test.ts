import { DateTime } from 'luxon';
import { describe, expect, it } from 'vitest';

import { Day } from '../day';
import { Zone } from '../zone';

const tokyo = Zone.resolve('Asia/Tokyo');
const newYork = Zone.resolve('America/New_York');
const utc = Zone.utc();

describe('Day.from', () => {
  it('answers a different day in different zones for the same instant', () => {
    // 2027-08-05 22:00Z is already the 6th in Tokyo and still the 5th in
    // New York. A Day that could be built without naming a zone would
    // have to pick one of these silently.
    const instant = DateTime.fromISO('2027-08-05T22:00:00Z', { zone: 'utc' });
    expect(Day.from(instant, tokyo).toString()).toBe('2027-08-06');
    expect(Day.from(instant, newYork).toString()).toBe('2027-08-05');
    expect(Day.from(instant, utc).toString()).toBe('2027-08-05');
  });

  it('reads a JS Date in the named zone rather than the host zone', () => {
    const instant = new Date(Date.UTC(2027, 7, 5, 22, 0, 0));
    expect(Day.from(instant, tokyo).toString()).toBe('2027-08-06');
    expect(Day.from(instant, newYork).toString()).toBe('2027-08-05');
  });

  it('reads unix seconds in the named zone', () => {
    const seconds = Math.floor(Date.UTC(2027, 7, 5, 22, 0, 0) / 1000);
    expect(Day.fromUnixSeconds(seconds, tokyo).toString()).toBe('2027-08-06');
    expect(Day.fromUnixSeconds(seconds, newYork).toString()).toBe('2027-08-05');
  });
});

describe('Day.parse', () => {
  it('accepts a well-formed date column', () => {
    const day = Day.parse('2027-08-05');
    expect(day?.year).toBe(2027);
    expect(day?.month).toBe(8);
    expect(day?.day).toBe(5);
  });

  it('rejects a date that does not exist rather than rolling it over', () => {
    // Luxon and Date both happily turn 2027-02-30 into March. Silently
    // accepting it puts a task on a day the user never chose.
    expect(Day.parse('2027-02-30')).toBeNull();
    expect(Day.parse('2027-13-01')).toBeNull();
  });

  it('accepts a real leap day and rejects a fake one', () => {
    expect(Day.parse('2028-02-29')?.toString()).toBe('2028-02-29');
    expect(Day.parse('2027-02-29')).toBeNull();
  });

  it('rejects malformed and absent input', () => {
    expect(Day.parse('2027-8-5')).toBeNull();
    expect(Day.parse('not a date')).toBeNull();
    expect(Day.parse(undefined)).toBeNull();
    expect(Day.parse('')).toBeNull();
  });
});

describe('Day boundaries', () => {
  it('starts a day at local midnight in the named zone', () => {
    const day = Day.of(2027, 8, 5);
    expect(day.start(tokyo).toUTC().toISO()).toBe('2027-08-04T15:00:00.000Z');
    expect(day.start(utc).toUTC().toISO()).toBe('2027-08-05T00:00:00.000Z');
  });

  it('ends exclusively at the next day start, giving a 24h window off DST', () => {
    const day = Day.of(2027, 8, 5);
    const span = day.endExclusive(tokyo).diff(day.start(tokyo), 'hours').hours;
    expect(span).toBe(24);
  });

  it('gives a 23-hour window on a spring-forward day', () => {
    // 2027-03-14 is the US spring-forward. A half-open day window has to
    // be 23 hours there; an implementation that added a fixed 24h would
    // reach an hour into the next day.
    const day = Day.of(2027, 3, 14);
    const span = day.endExclusive(newYork).diff(day.start(newYork), 'hours').hours;
    expect(span).toBe(23);
  });

  it('gives a 25-hour window on a fall-back day', () => {
    const day = Day.of(2027, 11, 7);
    const span = day.endExclusive(newYork).diff(day.start(newYork), 'hours').hours;
    expect(span).toBe(25);
  });

  it('places a wall-clock time on the day in the named zone', () => {
    const at = Day.of(2027, 8, 5).at(tokyo, 9, 30);
    expect(at.toUTC().toISO()).toBe('2027-08-05T00:30:00.000Z');
  });
});

describe('Day arithmetic', () => {
  it('adds and subtracts days across a month boundary', () => {
    expect(Day.of(2027, 8, 31).addDays(1).toString()).toBe('2027-09-01');
    expect(Day.of(2027, 9, 1).addDays(-1).toString()).toBe('2027-08-31');
  });

  it('crosses a leap day correctly', () => {
    expect(Day.of(2028, 2, 28).addDays(1).toString()).toBe('2028-02-29');
    expect(Day.of(2027, 2, 28).addDays(1).toString()).toBe('2027-03-01');
  });

  it('is unaffected by a DST transition', () => {
    // Adding a day across spring-forward must land on the next date, not
    // 23 hours later on the same one.
    expect(Day.of(2027, 3, 13).addDays(1).toString()).toBe('2027-03-14');
    expect(Day.of(2027, 3, 14).addDays(1).toString()).toBe('2027-03-15');
  });

  it('counts whole days between two days', () => {
    expect(Day.of(2027, 8, 10).diffDays(Day.of(2027, 8, 5))).toBe(5);
    expect(Day.of(2027, 8, 5).diffDays(Day.of(2027, 8, 10))).toBe(-5);
    expect(Day.of(2027, 3, 15).diffDays(Day.of(2027, 3, 13))).toBe(2);
  });

  it('reports the ISO weekday', () => {
    // 2027-08-05 is a Thursday.
    expect(Day.of(2027, 8, 5).weekday).toBe(4);
    expect(Day.of(2027, 8, 8).weekday).toBe(7);
  });
});

describe('Day serialisation', () => {
  it('zero-pads to the date-column form', () => {
    expect(Day.of(2027, 1, 2).toString()).toBe('2027-01-02');
    expect(Day.of(2027, 1, 2).dateColumn()).toBe('2027-01-02');
  });

  it('round-trips through parse', () => {
    const day = Day.of(2027, 1, 2);
    expect(Day.parse(day.toString())?.equals(day)).toBe(true);
  });

  it('compares by value', () => {
    expect(Day.of(2027, 1, 2).equals(Day.of(2027, 1, 2))).toBe(true);
    expect(Day.of(2027, 1, 2).equals(Day.of(2027, 1, 3))).toBe(false);
  });
});

describe('Day.today', () => {
  it('reads the clock in the named zone', () => {
    const now = new Date(Date.UTC(2027, 7, 5, 22, 0, 0));
    expect(Day.today(tokyo, now).toString()).toBe('2027-08-06');
    expect(Day.today(newYork, now).toString()).toBe('2027-08-05');
  });
});
