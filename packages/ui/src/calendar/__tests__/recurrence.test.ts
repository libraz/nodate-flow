import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';

import { DateTime, Settings } from 'luxon';
import { describe, expect, it } from 'vitest';

import { expandAllRecurrences, expandRecurrence } from '../recurrence';

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
    recurrenceEnd?: string;
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
      // The window bounds are read in UTC, not in whatever zone the
      // machine running the suite is in. The fixture is shared with the
      // Go expander, which reads them in UTC; a host-zone read here
      // would make the TypeScript side of a shared fixture agree or
      // disagree depending on where CI happens to run.
      const instances = expandRecurrence(
        fixture.event,
        DateTime.fromISO(fixture.rangeStart, { zone: 'utc' }),
        DateTime.fromISO(fixture.rangeEnd, { zone: 'utc' }),
      );

      expect(instances.map((i) => i.startAt.toUTC().toFormat("yyyy-MM-dd'T'HH:mm:ss'Z'"))).toEqual(
        fixture.expectedStartAt,
      );
    });
  }
});

/**
 * The expansion must not depend on where the machine running it is.
 *
 * The fixtures above are shared with the Go expander, and two of them
 * exist specifically to pin that an omitted timezone reads as UTC. On a
 * host that sits in a zone with no DST and a whole-hour offset east of
 * UTC — Asia/Tokyo, say — those two fixtures pass whether or not the
 * rule holds, because the host zone happens to give the same instants.
 * Running the suite there and seeing green therefore says nothing, and
 * which zone CI sits in is not a thing this repo controls.
 *
 * Sweeping the host zone turns that accident into a real check: the
 * zones below straddle UTC, include northern and southern DST, and
 * include the +14 and -11 extremes, so at least one of them disagrees
 * with UTC for every fixture that has a day boundary or a wall clock in
 * it.
 */
const HOST_ZONES = [
  'UTC',
  'America/New_York',
  'America/Los_Angeles',
  'Europe/London',
  'Asia/Tokyo',
  'Australia/Sydney',
  'Pacific/Kiritimati',
  'Pacific/Midway',
];

describe('expandRecurrence — invariant under the host zone', () => {
  const fixtures = loadRecurrenceGolden();

  it('loaded the shared fixtures', () => {
    // Guard against the loop below silently iterating nothing.
    expect(fixtures.length).toBeGreaterThanOrEqual(13);
  });

  for (const hostZone of HOST_ZONES) {
    describe(`host zone ${hostZone}`, () => {
      for (const fixture of fixtures) {
        it(fixture.name, () => {
          const previous = Settings.defaultZone;
          Settings.defaultZone = hostZone;
          try {
            const instances = expandRecurrence(
              fixture.event,
              DateTime.fromISO(fixture.rangeStart, { zone: 'utc' }),
              DateTime.fromISO(fixture.rangeEnd, { zone: 'utc' }),
            );
            expect(
              instances.map((i) => i.startAt.toUTC().toFormat("yyyy-MM-dd'T'HH:mm:ss'Z'")),
            ).toEqual(fixture.expectedStartAt);
          } finally {
            Settings.defaultZone = previous;
          }
        });
      }
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

describe('expandRecurrence — overridden starts', () => {
  // The second way an occurrence departs from its series: a separate row
  // replaces it and renders at its own, possibly moved, time. The master
  // emitting it as well is the same occurrence drawn twice.
  it('does not emit an occurrence a separate override row replaces', () => {
    const instances = expandRecurrence(
      {
        startAt: '2027-03-01T09:00:00Z',
        endAt: '2027-03-01T10:00:00Z',
        timezone: 'UTC',
        recurrenceRule: { freq: 'daily' as const, interval: 1 },
        overriddenStarts: ['2027-03-03T09:00:00Z'],
      },
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-03-05T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2027-03-01',
      '2027-03-02',
      '2027-03-04',
    ]);
  });

  it('accepts a bare date as an overridden start', () => {
    const instances = expandRecurrence(
      {
        startAt: '2027-03-01T09:00:00Z',
        endAt: '2027-03-01T10:00:00Z',
        timezone: 'UTC',
        recurrenceRule: { freq: 'daily' as const, interval: 1 },
        overriddenStarts: ['2027-03-02'],
      },
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-03-04T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2027-03-01',
      '2027-03-03',
    ]);
  });

  // A replaced occurrence still happens — it was moved, not cancelled —
  // so it consumes a count exactly as an ordinary occurrence does, which
  // is the same treatment cancellation gets. Neither kind of departure
  // lengthens the series at the far end.
  it('counts a replaced occurrence against COUNT, as a cancelled one is', () => {
    const instances = expandRecurrence(
      {
        startAt: '2027-03-01T09:00:00Z',
        endAt: '2027-03-01T10:00:00Z',
        timezone: 'UTC',
        recurrenceRule: { freq: 'daily' as const, interval: 1, count: 3 },
        recurrenceExceptions: ['2027-03-02T09:00:00Z'],
        overriddenStarts: ['2027-03-03T09:00:00Z'],
      },
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-04-01T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual(['2027-03-01']);
  });

  // An override may be moved anywhere, including out of the window being
  // expanded, so the starts it replaces arrive unfiltered: the ones inside
  // the window suppress, and the ones outside it are inert rather than
  // something to trim before calling.
  it('suppresses within the window while ignoring starts outside it', () => {
    const instances = expandRecurrence(
      {
        startAt: '2027-03-01T09:00:00Z',
        endAt: '2027-03-01T10:00:00Z',
        timezone: 'UTC',
        recurrenceRule: { freq: 'daily' as const, interval: 1 },
        overriddenStarts: ['2027-03-03T09:00:00Z', '2027-03-20T09:00:00Z'],
      },
      DateTime.fromISO('2027-03-02T00:00:00Z'),
      DateTime.fromISO('2027-03-05T00:00:00Z'),
    );
    expect(instances.map((i) => i.startAt.toUTC().toFormat('yyyy-MM-dd'))).toEqual([
      '2027-03-02',
      '2027-03-04',
    ]);
  });
});

describe('expandAllRecurrences — overridden starts', () => {
  // An override row has no rule of its own, so it flows through as an
  // ordinary event and renders at its moved time. Both halves have to hold
  // at once: the master drops the replaced occurrence, the override row
  // supplies it.
  it('passes an override row through while its master drops the occurrence', () => {
    const events = [
      {
        id: 'master',
        startAt: '2027-03-01T09:00:00Z',
        endAt: '2027-03-01T10:00:00Z',
        timezone: 'UTC',
        recurrenceRule: { freq: 'daily' as const, interval: 1 },
        overriddenStarts: ['2027-03-03T09:00:00Z'],
      },
      {
        id: 'override',
        startAt: '2027-03-03T14:00:00Z',
        endAt: '2027-03-03T15:00:00Z',
        timezone: 'UTC',
        recurrenceRule: null,
      },
    ];

    const expanded = expandAllRecurrences(
      events,
      DateTime.fromISO('2027-03-01T00:00:00Z'),
      DateTime.fromISO('2027-03-05T00:00:00Z'),
    );

    expect(expanded.map((e) => `${e.id} ${DateTime.fromISO(e.startAt).toUTC().toISO()}`)).toEqual([
      'master 2027-03-01T09:00:00.000Z',
      'master 2027-03-02T09:00:00.000Z',
      'master 2027-03-04T09:00:00.000Z',
      'override 2027-03-03T14:00:00.000Z',
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
