/**
 * Every write boundary that turns a wall clock into a stored value,
 * asserted under several host timezones.
 *
 * A single run proves almost nothing about this class of bug. The
 * failure being guarded is "the code read the machine's zone instead of
 * the data's", and on a machine in `Asia/Tokyo` — no DST, a whole-hour
 * offset — a great many wrong answers coincide with the right one, so
 * an assertion that passes there can be passing for the wrong reason.
 *
 * So the assertions below run in a child process per zone, and the list
 * deliberately includes a zone with DST, one at a half-hour offset, one
 * west of Greenwich and UTC itself. The child process is the whole
 * mechanism: `process.env.TZ` assigned inside a running process does
 * *not* move the host zone in this runner — `new Date()` and
 * `Intl.DateTimeFormat()` keep answering with whatever the process
 * started in — so a sweep written as a loop over `vi.stubEnv('TZ', …)`
 * would run the same host zone every iteration and report a clean pass
 * over one machine's answer.
 *
 * The sweep also has to prove it is sweeping. Each child records the
 * host zone it actually observed plus a deliberately host-zone-dependent
 * probe value, and the parent fails unless those genuinely differ across
 * the run. Without that, a sweep whose environment plumbing silently
 * stopped working looks exactly like a passing one.
 */

import { spawnSync } from 'node:child_process';
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { expandAllRecurrences, type RecurrenceRule } from '@nodate-flow/ui/calendar';
import { Day, Zone } from '@nodate-flow/ui/time';
import { DateTime } from 'luxon';
import { describe, expect, it } from 'vitest';

import { eventDayKeys } from '../../features/calendar-events/lib/event-days';
import { morningIn } from '../../features/inbox/snooze-popover';
import { defaultRange } from '../../features/public-shares/add-events-dialog';
import {
  allDayToUnix,
  endOfDayIso,
  eventDateKey,
  startOfDayIso,
  todayKey,
  unixToWallClock,
  wallClockToUnix,
} from '../date-utils';
import { isOverdue } from '../format';

/**
 * The host zones the assertions are replayed under.
 *
 * `Australia/Adelaide` is the half-hour offset and `Europe/Berlin` and
 * `America/New_York` are the two that observe DST on different dates;
 * `Pacific/Kiritimati` is +14, far enough east that its calendar day is
 * ahead of every other entry for part of the day. UTC is here because a
 * bug that only shows up away from zero offset is still a bug, and its
 * absence would let one hide.
 */
const SWEEP_ZONES = [
  'UTC',
  'Asia/Tokyo',
  'Asia/Kolkata',
  'Australia/Adelaide',
  'Europe/Berlin',
  'America/New_York',
  'Pacific/Kiritimati',
] as const;

/** Set in the children so they run the assertions instead of the sweep. */
const CHILD_ENV = 'NF_HOST_ZONE_SWEEP';
/** Where a child writes what it observed, for the parent to compare. */
const OBSERVED_ENV = 'NF_HOST_ZONE_SWEEP_OBSERVED';

const childZone = process.env[CHILD_ENV];

/* ── the zones the product code is asked about ──────────────────── */

const tokyo = Zone.resolve('Asia/Tokyo');
const newYork = Zone.resolve('America/New_York');
const berlin = Zone.resolve('Europe/Berlin');
const kolkata = Zone.resolve('Asia/Kolkata');
const utc = Zone.utc();

if (childZone) {
  describe(`write boundaries under host zone ${childZone}`, () => {
    it('is actually running in the host zone the sweep asked for', () => {
      // The load-bearing assertion of the whole file. If this stops
      // holding, every other assertion below is being made seven times
      // about one machine.
      //
      // Compared by offset rather than by name because a runtime is free
      // to answer with a zone's alias — `Asia/Kolkata` comes back as
      // `Asia/Calcutta` — and a name check would fail on a host that is
      // in exactly the right zone. Two probe instants, so the comparison
      // also pins the DST rule and not just the January offset.
      const host = Zone.browser();
      for (const probe of [Date.UTC(2027, 0, 15), Date.UTC(2027, 6, 15)]) {
        expect(offsetAt(probe, host.name)).toBe(offsetAt(probe, childZone));
      }
      recordObservation(host.name);
    });

    /* ── event dialog: submit ─────────────────────────────────── */

    it('resolves a submitted wall clock in the event zone, not the host', () => {
      // 2027-08-05 09:00 in Tokyo is 00:00Z; the same wall clock in New
      // York is 13:00Z. Neither depends on where the person typing it
      // is sitting, which is precisely what `new Date(y, m, d, hh, mm)`
      // makes it depend on.
      expect(wallClockToUnix('2027-08-05', '09:00', tokyo)).toBe(
        Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000,
      );
      expect(wallClockToUnix('2027-08-05', '09:00', newYork)).toBe(
        Date.UTC(2027, 7, 5, 13, 0, 0, 0) / 1000,
      );
      // A half-hour offset, which an implementation that rounds to whole
      // hours somewhere would get away with in every other entry here.
      expect(wallClockToUnix('2027-08-05', '09:00', kolkata)).toBe(
        Date.UTC(2027, 7, 5, 3, 30, 0, 0) / 1000,
      );
    });

    it('reads a summer and a winter wall clock in the same zone differently', () => {
      // Berlin is +02:00 in August and +01:00 in January. A fixed offset
      // — including "whatever offset the host had when the module
      // loaded" — gets one of these wrong.
      expect(wallClockToUnix('2027-08-05', '12:00', berlin)).toBe(
        Date.UTC(2027, 7, 5, 10, 0, 0, 0) / 1000,
      );
      expect(wallClockToUnix('2027-01-05', '12:00', berlin)).toBe(
        Date.UTC(2027, 0, 5, 11, 0, 0, 0) / 1000,
      );
    });

    /* ── event dialog: seeding the edit form ──────────────────── */

    it('seeds the edit form in the same zone it submits from', () => {
      // The pair that has to move together. Seeding in one zone and
      // submitting in another silently re-times every event that is
      // opened and saved, by the offset between them — an edit of data
      // the user never touched.
      for (const zone of [tokyo, newYork, berlin, kolkata, utc]) {
        for (const [date, time] of [
          ['2027-08-05', '09:00'],
          ['2027-01-05', '23:45'],
          ['2027-03-28', '00:15'],
        ] as const) {
          const instant = wallClockToUnix(date, time, zone);
          expect(unixToWallClock(instant, zone)).toEqual({ date, time });
        }
      }
    });

    it('shows a stored instant as the wall clock its own zone gives it', () => {
      const instant = Date.UTC(2027, 7, 5, 22, 0, 0, 0) / 1000;
      expect(unixToWallClock(instant, tokyo)).toEqual({ date: '2027-08-06', time: '07:00' });
      expect(unixToWallClock(instant, newYork)).toEqual({ date: '2027-08-05', time: '18:00' });
    });

    /* ── all-day rows stay dates ──────────────────────────────── */

    it('keeps an all-day date at midnight UTC whoever is looking', () => {
      // An all-day row is a date, and a date is the same square for
      // everyone. This is the one boundary that must *not* move with the
      // effective zone either.
      expect(allDayToUnix('2027-08-05')).toBe(Date.UTC(2027, 7, 5, 0, 0, 0, 0) / 1000);
      expect(eventDateKey(allDayToUnix('2027-08-05'), true, newYork)).toBe('2027-08-05');
      expect(eventDateKey(allDayToUnix('2027-08-05'), true, tokyo)).toBe('2027-08-05');
    });

    /* ── calendar route: the grid's fetch window ──────────────── */

    it('bounds the grid range on the effective zone rather than the host', () => {
      expect(startOfDayIso('2027-08-05', tokyo)).toBe('2027-08-04T15:00:00.000Z');
      expect(startOfDayIso('2027-08-05', newYork)).toBe('2027-08-05T04:00:00.000Z');
      expect(endOfDayIso('2027-08-05', tokyo)).toBe('2027-08-05T14:59:59.999Z');
      expect(endOfDayIso('2027-08-05', newYork)).toBe('2027-08-06T03:59:59.999Z');
    });

    it('covers the whole day and nothing of the next one', () => {
      for (const zone of [tokyo, newYork, berlin, kolkata, utc]) {
        // A DST-shortened day is 23 hours and a lengthened one 25, so a
        // window built by adding a fixed 86,399,999 ms drops an hour or
        // reaches into the following day exactly twice a year.
        for (const day of ['2027-03-28', '2027-10-31', '2027-08-05']) {
          const start = Date.parse(startOfDayIso(day, zone));
          const end = Date.parse(endOfDayIso(day, zone));
          const nextStart = Date.parse(startOfDayIso(nextDay(day), zone));
          expect(end).toBe(nextStart - 1);
          expect(end).toBeGreaterThan(start);
        }
      }
    });

    it('fetches the window whose edges are the days it lays out', () => {
      // The coupling, not either half of it. The grid picks its edges in
      // one zone and files the events it gets back under day boundaries
      // in another, and each half is only correct relative to the other:
      // a window fetched on Berlin edges and laid out on Tokyo days is
      // internally consistent and still short of data in the first and
      // last cell. Asserting the round trip is what makes the two halves
      // one decision rather than two that happen to agree today.
      for (const zone of [tokyo, newYork, berlin, kolkata, utc]) {
        for (const day of ['2027-03-28', '2027-10-31', '2027-08-05']) {
          const firstInstant = Date.parse(startOfDayIso(day, zone)) / 1000;
          const lastInstant = Date.parse(endOfDayIso(day, zone)) / 1000;
          expect(eventDateKey(firstInstant, false, zone)).toBe(day);
          expect(eventDateKey(lastInstant, false, zone)).toBe(day);
          // One second before the window opens is the previous day, so
          // the edge is the boundary rather than somewhere inside it.
          expect(eventDateKey(firstInstant - 1, false, zone)).not.toBe(day);
        }
      }
    });

    it('expands recurrences on window bounds that carry no day meaning', () => {
      // The calendar route hands the expander the same range object it
      // fetched with, read as UTC. That is only inert while the expander
      // uses the bounds for instant comparisons alone — the moment one
      // becomes day-shaped, the window silently becomes the reader's.
      // Asserted from the consumer side because it is the route's
      // assumption that would break, not the expander's own contract.
      //
      // The upper bound is swept across a whole day rather than fixed.
      // A day-shaped bound snaps to local midnight, which across these
      // zones spans about 26 hours, so whether that snap crosses an
      // occurrence depends entirely on where the bound sits relative to
      // one. A single hand-picked pair can miss for every zone at once
      // and report a guard that has never seen the thing it names;
      // stepping the bound guarantees some offset in the sweep puts an
      // occurrence inside the spread.
      const event = {
        event: { id: 'e' },
        startAt: '2027-08-01T09:00:00+09:00',
        endAt: '2027-08-01T10:00:00+09:00',
        timezone: 'Asia/Tokyo',
        recurrenceRule: { freq: 'daily' } as RecurrenceRule,
      };
      const from = Date.UTC(2027, 7, 1);
      const expanded = (to: number, boundsZone: string): number[] =>
        expandAllRecurrences(
          [event],
          DateTime.fromMillis(from, { zone: boundsZone }),
          DateTime.fromMillis(to, { zone: boundsZone }),
        ).map((i) => Date.parse(i.startAt));

      const hour = 3_600_000;
      for (let step = 0; step < 24 * 2; step++) {
        const to = Date.UTC(2027, 7, 20) + step * (hour / 2);
        const inUtc = expanded(to, 'UTC');
        for (const boundsZone of SWEEP_ZONES) {
          expect(expanded(to, boundsZone), `${boundsZone} at +${step / 2}h`).toEqual(inUtc);
        }
      }
      // The sweep has to be looking at a non-empty expansion, or every
      // comparison above is between two empty arrays.
      expect(expanded(Date.UTC(2027, 7, 20), 'UTC').length).toBeGreaterThan(15);
    });

    /* ── public-share picker ──────────────────────────────────── */

    it('opens the share picker on a window whose bounds match its labels', () => {
      for (const zone of [tokyo, newYork, kolkata]) {
        const range = defaultRange(zone);
        expect(range.from).toBe(todayKey(zone));
        expect(Day.parse(range.to)?.diffDays(Day.parse(range.from) as Day)).toBe(90);
        // The label and the instant sent for it have to name the same
        // day: local midnight reinterpreted as UTC is what put the two a
        // day apart, and the same strings key the query cache.
        expect(startOfDayIso(range.from, zone)).toBe(
          (Day.parse(range.from) as Day).start(zone).toUTC().toISO(),
        );
      }
    });

    /* ── snooze ───────────────────────────────────────────────── */

    it('snoozes to 9am on the day the user is in, not the machine', () => {
      for (const zone of [tokyo, newYork, berlin, kolkata, utc]) {
        for (const offset of [1, 7]) {
          const target = morningIn(zone, offset);
          const wall = unixToWallClock(target, zone);
          expect(wall.time).toBe('09:00');
          expect(wall.date).toBe(Day.today(zone).addDays(offset).toString());
        }
      }
    });

    /* ── "is this today" ──────────────────────────────────────── */

    it('answers today, yesterday and tomorrow in the effective zone', () => {
      const instant = new Date(Date.UTC(2027, 7, 5, 22, 0, 0, 0));
      // 22:00Z is already the 6th in Tokyo and still the 5th in New
      // York. This is the disagreement that made the grid highlight one
      // cell and file its events under another.
      expect(todayKey(tokyo, instant)).toBe('2027-08-06');
      expect(todayKey(newYork, instant)).toBe('2027-08-05');
    });

    it('marks a task overdue by the same day boundary the calendar draws', () => {
      for (const zone of [tokyo, newYork, berlin, kolkata, utc]) {
        const today = Day.today(zone);
        expect(isOverdue(today.toString(), zone)).toBe(false);
        expect(isOverdue(today.addDays(-1).toString(), zone)).toBe(true);
        expect(isOverdue(today.addDays(1).toString(), zone)).toBe(false);
      }
    });

    it('files a timed event under the day its zone puts it on', () => {
      const event = {
        startAt: Date.UTC(2027, 7, 5, 22, 0, 0, 0) / 1000,
        endAt: Date.UTC(2027, 7, 5, 23, 0, 0, 0) / 1000,
      } as never;
      expect(eventDayKeys(event, tokyo)).toEqual(['2027-08-06']);
      expect(eventDayKeys(event, newYork)).toEqual(['2027-08-05']);
    });
  });
} else {
  describe('host-zone sweep', () => {
    it('replays the write-boundary assertions under every zone, and varies the host', () => {
      const dir = mkdtempSync(path.join(tmpdir(), 'nf-zone-sweep-'));
      try {
        for (const zone of SWEEP_ZONES) {
          const result = spawnSync(
            'bun',
            ['run', 'vitest', 'run', '--reporter=dot', relativeSelf()],
            {
              cwd: process.cwd(),
              encoding: 'utf8',
              env: {
                ...process.env,
                TZ: zone,
                [CHILD_ENV]: zone,
                [OBSERVED_ENV]: path.join(dir, zone.replace(/\//g, '_')),
              },
            },
          );
          expect(result.status, `${zone}\n${result.stdout ?? ''}\n${result.stderr ?? ''}`).toBe(0);
        }

        // Proof that the sweep swept. Each child recorded the host zone
        // it saw and a value read through host-local getters; if the
        // environment plumbing ever stops taking effect, these collapse
        // to one entry and the sweep is decoration.
        const observed = readdirSync(dir).map(
          (f) => JSON.parse(readFileSync(path.join(dir, f), 'utf8')) as Observation,
        );
        expect(observed).toHaveLength(SWEEP_ZONES.length);
        expect(new Set(observed.map((o) => o.hostZone)).size).toBe(SWEEP_ZONES.length);
        // The zones above put a single instant on five or more distinct
        // local wall clocks. If the probe collapses, the children were
        // all in one zone whatever they reported.
        expect(new Set(observed.map((o) => o.hostDependentProbe)).size).toBeGreaterThanOrEqual(5);
      } finally {
        rmSync(dir, { recursive: true, force: true });
      }
      // Seven child vitest runs; the default 5s would fail on timing
      // rather than on anything this file is about.
    }, 180_000);
  });
}

interface Observation {
  hostZone: string;
  hostDependentProbe: string;
}

/**
 * Record what this child actually saw, so the parent can prove the
 * zones differed rather than take the plumbing on trust.
 *
 * The probe is read through host-local getters on purpose: it is the
 * control that has to vary. Nothing in the product may look like this.
 */
function recordObservation(hostZone: string): void {
  const target = process.env[OBSERVED_ENV];
  if (!target) return;
  const probeInstant = new Date(Date.UTC(2027, 7, 5, 22, 0, 0, 0));
  const observation: Observation = {
    hostZone,
    hostDependentProbe: `${probeInstant.getDate()}T${probeInstant.getHours()}:${probeInstant.getMinutes()}`,
  };
  writeFileSync(target, JSON.stringify(observation), 'utf8');
}

/** Minutes east of UTC that `zone` is at `millis`. */
function offsetAt(millis: number, zone: string): number {
  return DateTime.fromMillis(millis, { zone }).offset;
}

/** The next calendar day of a `YYYY-MM-DD` key. */
function nextDay(key: string): string {
  return (Day.parse(key) as Day).addDays(1).toString();
}

/** This file, relative to the package root the child runs vitest from. */
function relativeSelf(): string {
  return path.relative(process.cwd(), new URL(import.meta.url).pathname);
}
