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
