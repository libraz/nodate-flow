import { DateTime } from 'luxon';

import type { Zone } from './zone';

const DAY_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/;

/**
 * A wall-clock calendar day: a year, a month and a day number, with no
 * instant and no zone of its own.
 *
 * This is the type behind a `*_on` column and behind every "which square
 * of the grid" question. It is deliberately not a `Date` and not a
 * `DateTime`. Both of those are instants, and an instant only becomes a
 * calendar day once somebody says in which zone — so a `Date` standing
 * in for a day carries a zone it never declared, and reading it back
 * with local getters silently answers in whatever zone the reader is in.
 * That is how an all-day event filed on the 5th shows up on the 4th one
 * timezone to the west.
 *
 * Every method here that crosses between a day and an instant takes a
 * [Zone] as a required argument. There is no overload that omits it and
 * no default, because the default is exactly the bug: a day boundary is
 * only meaningful relative to a zone, and the one the host happens to be
 * in is rarely the one the data means.
 *
 * Mirrors `Day` in `packages/go-shared/region`.
 */
export class Day {
  readonly #year: number;
  readonly #month: number;
  readonly #day: number;

  private constructor(year: number, month: number, day: number) {
    this.#year = year;
    this.#month = month;
    this.#day = day;
  }

  get year(): number {
    return this.#year;
  }

  /** 1-12. */
  get month(): number {
    return this.#month;
  }

  /** 1-31. */
  get day(): number {
    return this.#day;
  }

  /** ISO weekday, Monday = 1 through Sunday = 7. */
  get weekday(): number {
    return this.#asUtcNoon().weekday;
  }

  /**
   * `YYYY-MM-DD` — the form a `*_on` column and a grid key take.
   *
   * Note this is also `toString()`, so a Day interpolated into a
   * template literal produces the column form rather than the default
   * object rendering.
   */
  toString(): string {
    const m = String(this.#month).padStart(2, '0');
    const d = String(this.#day).padStart(2, '0');
    return `${this.#year}-${m}-${d}`;
  }

  /** Alias of [toString], named for the column it serialises to. */
  dateColumn(): string {
    return this.toString();
  }

  equals(other: Day): boolean {
    return this.#year === other.#year && this.#month === other.#month && this.#day === other.#day;
  }

  /**
   * The first instant of this day in `zone`.
   *
   * On a spring-forward date where local midnight does not exist, luxon
   * returns the first instant that does, which is the reading a day
   * boundary wants.
   */
  start(zone: Zone): DateTime {
    return DateTime.fromObject(
      { year: this.#year, month: this.#month, day: this.#day },
      { zone: zone.name },
    ).startOf('day');
  }

  /**
   * The first instant of the *next* day in `zone` — the exclusive upper
   * bound of a half-open day range.
   *
   * Exclusive rather than "end of day" because an inclusive bound has to
   * pick a granularity (23:59:59? .999?) and every choice of it drops or
   * double-counts something at the edge.
   */
  endExclusive(zone: Zone): DateTime {
    return this.addDays(1).start(zone);
  }

  /** The instant of a wall-clock time on this day in `zone`. */
  at(zone: Zone, hour: number, minute = 0, second = 0): DateTime {
    return DateTime.fromObject(
      {
        year: this.#year,
        month: this.#month,
        day: this.#day,
        hour,
        minute,
        second,
      },
      { zone: zone.name },
    );
  }

  /** The day `n` days after this one (negative goes back). */
  addDays(n: number): Day {
    const shifted = this.#asUtcNoon().plus({ days: n });
    return new Day(shifted.year, shifted.month, shifted.day);
  }

  /** Whole days from `other` to this day. Positive when this is later. */
  diffDays(other: Day): number {
    return Math.round(this.#asUtcNoon().diff(other.#asUtcNoon(), 'days').days);
  }

  // Day arithmetic is done at UTC noon so that adding days can never be
  // perturbed by a DST transition: no zone shifts by twelve hours, so
  // noon plus or minus n days always lands on the intended date.
  #asUtcNoon(): DateTime {
    return DateTime.fromObject(
      { year: this.#year, month: this.#month, day: this.#day, hour: 12 },
      { zone: 'UTC' },
    );
  }

  /** The day `year-month-day`, with no zone attached. */
  static of(year: number, month: number, day: number): Day {
    return new Day(year, month, day);
  }

  /**
   * The calendar day an instant falls on, read in `zone`.
   *
   * The zone is required and has no default: "which day is this instant"
   * has a different answer in every zone, and picking the host's is a
   * choice, not an absence of one.
   */
  static from(instant: DateTime | Date, zone: Zone): Day {
    const local =
      instant instanceof Date
        ? DateTime.fromJSDate(instant, { zone: zone.name })
        : instant.setZone(zone.name);
    return new Day(local.year, local.month, local.day);
  }

  /** The calendar day a unix-seconds instant falls on, read in `zone`. */
  static fromUnixSeconds(seconds: number, zone: Zone): Day {
    return Day.from(DateTime.fromSeconds(seconds, { zone: zone.name }), zone);
  }

  /**
   * Parse a `YYYY-MM-DD` string, or `null` if it is not one or names a
   * date that does not exist.
   *
   * Strict on both counts: a `*_on` value that fails to parse is a bug
   * upstream, and silently substituting today or the epoch for it turns
   * that bug into wrong data on a screen.
   */
  static parse(value: string | null | undefined): Day | null {
    if (!value) return null;
    const match = DAY_PATTERN.exec(value.trim());
    if (!match) return null;
    const year = Number(match[1]);
    const month = Number(match[2]);
    const day = Number(match[3]);
    const probe = DateTime.fromObject({ year, month, day }, { zone: 'UTC' });
    if (!probe.isValid || probe.year !== year || probe.month !== month || probe.day !== day) {
      return null;
    }
    return new Day(year, month, day);
  }

  /** Today's calendar day in `zone`. */
  static today(zone: Zone, now: Date = new Date()): Day {
    return Day.from(now, zone);
  }
}
