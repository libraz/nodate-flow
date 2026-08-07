import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';

import { DateTime } from 'luxon';
import { describe, expect, it } from 'vitest';

import { expandRecurrence } from '../recurrence';

interface RecurrenceGoldenFixture {
  name: string;
  event: {
    startAt: string;
    endAt: string;
    timezone?: string;
    recurrenceRule: {
      freq: 'daily' | 'weekly' | 'monthly' | 'yearly';
      interval?: number;
      byDay?: string[];
      byMonthDay?: number[];
      bySetPos?: number;
      until?: string;
      count?: number;
    };
    recurrenceExceptions?: string[];
  };
  rangeStart: string;
  rangeEnd: string;
  expectedStartAt: string[];
}

function loadRecurrenceGolden(): RecurrenceGoldenFixture[] {
  let dir = process.cwd();
  for (;;) {
    const candidate = path.join(dir, 'testdata', 'recurrence_golden.json');
    if (existsSync(candidate)) {
      return JSON.parse(readFileSync(candidate, 'utf8')) as RecurrenceGoldenFixture[];
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      throw new Error('could not find testdata/recurrence_golden.json');
    }
    dir = parent;
  }
}

describe('expandRecurrence — timezone awareness', () => {
  it('preserves wall-clock time across a US DST spring-forward transition', () => {
    // DST in America/New_York switches 2026-03-08 02:00 -> 03:00.
    // A weekly 09:00 NYC event on 2026-03-01 should still be 09:00 local on 2026-03-15.
    const event = {
      startAt: '2026-03-01T09:00:00-05:00',
      endAt: '2026-03-01T10:00:00-05:00',
      timezone: 'America/New_York',
      recurrenceRule: { freq: 'weekly' as const, interval: 1 },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-03-01T00:00:00Z'),
      DateTime.fromISO('2026-03-22T00:00:00Z'),
    );
    const localHours = instances.map((i) => i.startAt.setZone('America/New_York').hour);
    expect(localHours).toEqual([9, 9, 9]);
    // Post-DST instances should carry the -04:00 offset, not the source -05:00.
    const post = instances[2]?.startAt.setZone('America/New_York');
    expect(post?.offset).toBe(-4 * 60);
  });

  it('preserves wall-clock time across a Europe/London DST transition', () => {
    // 2026-03-29 01:00 GMT -> 02:00 BST. A daily 08:00 London event
    // must stay at 08:00 local before and after the transition.
    const event = {
      startAt: '2026-03-28T08:00:00+00:00',
      endAt: '2026-03-28T09:00:00+00:00',
      timezone: 'Europe/London',
      recurrenceRule: { freq: 'daily' as const, interval: 1, count: 3 },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-03-27T00:00:00Z'),
      DateTime.fromISO('2026-03-31T00:00:00Z'),
    );
    const localHours = instances.map((i) => i.startAt.setZone('Europe/London').hour);
    expect(localHours).toEqual([8, 8, 8]);
  });

  it('without a timezone field still expands with the instant preserved', () => {
    const event = {
      startAt: '2026-03-01T09:00:00-05:00',
      endAt: '2026-03-01T10:00:00-05:00',
      recurrenceRule: { freq: 'weekly' as const, interval: 1, count: 2 },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-03-01T00:00:00Z'),
      DateTime.fromISO('2026-03-22T00:00:00Z'),
    );
    expect(instances).toHaveLength(2);
    // Instants are 7 days apart regardless of which zone we render in.
    const delta = (instances[1]?.startAt.toMillis() ?? 0) - (instances[0]?.startAt.toMillis() ?? 0);
    expect(delta).toBe(7 * 24 * 60 * 60 * 1000);
  });
});

describe('expandRecurrence — shared golden fixtures', () => {
  for (const fixture of loadRecurrenceGolden()) {
    it(fixture.name, () => {
      const instances = expandRecurrence(
        fixture.event,
        DateTime.fromISO(fixture.rangeStart),
        DateTime.fromISO(fixture.rangeEnd),
      );

      expect(instances.map((i) => i.startAt.toUTC().toFormat("yyyy-MM-dd'T'HH:mm:ss'Z'"))).toEqual(
        fixture.expectedStartAt,
      );
    });
  }
});

describe('expandRecurrence — weekly byDay expansion', () => {
  it('expands every listed weekday within each week (weekdays preset)', () => {
    // 2026-07-06 is a Monday. WEEKLY;BYDAY=MO,TU,WE,TH,FR must produce five
    // occurrences per week, not just the DTSTART weekday.
    const event = {
      startAt: '2026-07-06T09:00:00Z',
      endAt: '2026-07-06T10:00:00Z',
      recurrenceRule: {
        freq: 'weekly' as const,
        interval: 1,
        byDay: ['MO', 'TU', 'WE', 'TH', 'FR'],
      },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-07-06T00:00:00Z'),
      DateTime.fromISO('2026-07-20T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2026-07-06',
      '2026-07-07',
      '2026-07-08',
      '2026-07-09',
      '2026-07-10',
      '2026-07-13',
      '2026-07-14',
      '2026-07-15',
      '2026-07-16',
      '2026-07-17',
    ]);
  });

  it('applies interval to the ISO week, keeping all listed weekdays of included weeks', () => {
    // Every second week, Monday and Wednesday: weeks starting Jul 6 and Jul 20.
    const event = {
      startAt: '2026-07-06T09:00:00Z',
      endAt: '2026-07-06T10:00:00Z',
      recurrenceRule: { freq: 'weekly' as const, interval: 2, byDay: ['MO', 'WE'] },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-07-06T00:00:00Z'),
      DateTime.fromISO('2026-08-03T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2026-07-06',
      '2026-07-08',
      '2026-07-20',
      '2026-07-22',
    ]);
  });

  it('starts mid-week without emitting listed weekdays before DTSTART', () => {
    // DTSTART on Wednesday 2026-07-08: the Monday of the same week is in the
    // past relative to DTSTART and must not appear. count applies to actual
    // occurrences.
    const event = {
      startAt: '2026-07-08T09:00:00Z',
      endAt: '2026-07-08T10:00:00Z',
      recurrenceRule: { freq: 'weekly' as const, byDay: ['MO', 'WE', 'FR'], count: 5 },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-07-01T00:00:00Z'),
      DateTime.fromISO('2026-08-01T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2026-07-08',
      '2026-07-10',
      '2026-07-13',
      '2026-07-15',
      '2026-07-17',
    ]);
  });
});

describe('expandRecurrence — anchor-based month/year arithmetic', () => {
  it('monthly Jan 31 clamps to each month last-valid day for 12 months without drifting to the 28th', () => {
    // The anchor-based expansion must derive every occurrence from Jan 31, so
    // February clamps to the 28th while March/May/etc. recover the 31st. A
    // chained implementation would stick at the 28th from February onward.
    const event = {
      startAt: '2026-01-31T09:00:00Z',
      endAt: '2026-01-31T10:00:00Z',
      recurrenceRule: { freq: 'monthly' as const, count: 12 },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-01-01T00:00:00Z'),
      DateTime.fromISO('2027-01-01T00:00:00Z'),
    );
    const days = instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'));
    expect(days).toEqual([
      '2026-01-31',
      '2026-02-28',
      '2026-03-31',
      '2026-04-30',
      '2026-05-31',
      '2026-06-30',
      '2026-07-31',
      '2026-08-31',
      '2026-09-30',
      '2026-10-31',
      '2026-11-30',
      '2026-12-31',
    ]);
    // February clamps to its last valid day; the rest of the year does not
    // inherit that clamp (no 28th drift after February).
    expect(days[1]).toBe('2026-02-28');
    expect(days[3]).toBe('2026-04-30');
    expect(days.slice(2).filter((d) => d.endsWith('-28'))).toEqual([]);
  });

  it('yearly Feb 29 clamps per occurrence and returns to Feb 29 on leap years', () => {
    // Chained arithmetic would stick at Feb 28 after the first clamp; the
    // anchor-based expansion recovers Feb 29 in 2028.
    const event = {
      startAt: '2024-02-29T09:00:00Z',
      endAt: '2024-02-29T10:00:00Z',
      recurrenceRule: { freq: 'yearly' as const, count: 5 },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2024-01-01T00:00:00Z'),
      DateTime.fromISO('2029-01-01T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2024-02-29',
      '2025-02-28',
      '2026-02-28',
      '2027-02-28',
      '2028-02-29',
    ]);
  });
});

describe('expandRecurrence — UNTIL bounds', () => {
  it('includes the occurrence landing on a date-only UNTIL day', () => {
    const event = {
      startAt: '2026-07-08T09:00:00Z',
      endAt: '2026-07-08T10:00:00Z',
      recurrenceRule: { freq: 'daily' as const, until: '2026-07-10' },
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-07-01T00:00:00Z'),
      DateTime.fromISO('2026-08-01T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2026-07-08',
      '2026-07-09',
      '2026-07-10',
    ]);
  });

  it('treats a datetime UNTIL as an exact inclusive instant', () => {
    const base = {
      startAt: '2026-07-08T09:00:00Z',
      endAt: '2026-07-08T10:00:00Z',
    };
    const rangeStart = DateTime.fromISO('2026-07-01T00:00:00Z');
    const rangeEnd = DateTime.fromISO('2026-08-01T00:00:00Z');

    const onInstant = expandRecurrence(
      { ...base, recurrenceRule: { freq: 'daily' as const, until: '2026-07-10T09:00:00Z' } },
      rangeStart,
      rangeEnd,
    );
    expect(onInstant).toHaveLength(3);

    const justBefore = expandRecurrence(
      { ...base, recurrenceRule: { freq: 'daily' as const, until: '2026-07-10T08:59:59Z' } },
      rangeStart,
      rangeEnd,
    );
    expect(justBefore).toHaveLength(2);
  });
});

describe('expandRecurrence — empty filters', () => {
  it('treats empty byDay/byMonthDay arrays as unspecified filters', () => {
    const event = {
      startAt: '2026-07-01T09:00:00Z',
      endAt: '2026-07-01T10:00:00Z',
      recurrenceRule: {
        freq: 'daily' as const,
        interval: 1,
        count: 2,
        byDay: [],
        byMonthDay: [],
      },
    };

    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2026-07-01T00:00:00Z'),
      DateTime.fromISO('2026-07-04T00:00:00Z'),
    );

    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2026-07-01',
      '2026-07-02',
    ]);
  });
});

describe('expandRecurrence — recurrenceEnd', () => {
  // recurrence_end lives in its own column beside the rule, not inside
  // it. An expander that reads only the rule keeps drawing a series the
  // API was told to stop — on the calendar and on the public share page
  // — with no way to end it from the UI.
  it('stops the series at recurrenceEnd', () => {
    const event = {
      startAt: '2027-03-01T09:00:00Z',
      endAt: '2027-03-01T10:00:00Z',
      timezone: 'UTC',
      recurrenceRule: { freq: 'daily' as const, interval: 1 },
      recurrenceEnd: '2027-03-03T23:59:59Z',
    };
    const instances = expandRecurrence(
      event,
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-04-01T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2027-03-01',
      '2027-03-02',
      '2027-03-03',
    ]);
  });

  // Two independent upper bounds; the earlier one ends the series.
  // Taking the later would run a series past a date one of them says it
  // stops, which is the same silent overrun in the other direction.
  it('takes the earlier of recurrenceEnd and the rule UNTIL', () => {
    const base = {
      startAt: '2027-03-01T09:00:00Z',
      endAt: '2027-03-01T10:00:00Z',
      timezone: 'UTC',
    };
    const range = [
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-04-01T00:00:00Z'),
    ] as const;

    const endEarlier = expandRecurrence(
      {
        ...base,
        recurrenceRule: { freq: 'daily' as const, interval: 1, until: '2027-03-10' },
        recurrenceEnd: '2027-03-02T23:59:59Z',
      },
      range[0],
      range[1],
    );
    expect(endEarlier).toHaveLength(2);

    const untilEarlier = expandRecurrence(
      {
        ...base,
        recurrenceRule: { freq: 'daily' as const, interval: 1, until: '2027-03-02' },
        recurrenceEnd: '2027-03-20T23:59:59Z',
      },
      range[0],
      range[1],
    );
    expect(untilEarlier).toHaveLength(2);
  });

  it('leaves a series without recurrenceEnd unbounded by it', () => {
    const instances = expandRecurrence(
      {
        startAt: '2027-03-01T09:00:00Z',
        endAt: '2027-03-01T10:00:00Z',
        timezone: 'UTC',
        recurrenceRule: { freq: 'daily' as const, interval: 1, count: 5 },
      },
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-04-01T00:00:00Z'),
    );
    expect(instances).toHaveLength(5);
  });
});
