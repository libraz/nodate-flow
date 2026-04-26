/**
 * Property-based tests for format utilities using fast-check.
 *
 * These complement the example-based tests in format.test.ts by
 * verifying invariants that must hold across all possible inputs.
 */

import fc from 'fast-check';
import { describe, expect, it, vi } from 'vitest';

import {
  formatDate,
  formatDateNullable,
  formatDateOnly,
  formatDateTime,
  formatEpoch,
  formatEpochDateTime,
  isOverdue,
  isZeroTime,
} from '../format';

describe('formatDate properties', () => {
  it('never throws for any string input', () => {
    fc.assert(
      fc.property(fc.string(), (input) => {
        // formatDate must always return a string, never throw.
        const result = formatDate(input, 'en-US');
        expect(typeof result).toBe('string');
      }),
      { numRuns: 500 },
    );
  });

  it('returns a non-empty string for any non-empty input', () => {
    // formatDate is documented to return the raw input when parsing
    // fails. For an empty input that is also an empty output, which is
    // a legal pass-through and not a contract violation. Constrain the
    // generator to non-empty strings so the property describes the
    // intended invariant ("output preserves at least one character of
    // recognisable input").
    fc.assert(
      fc.property(fc.string({ minLength: 1 }), (input) => {
        const result = formatDate(input, 'en-US');
        expect(result.length).toBeGreaterThan(0);
      }),
      { numRuns: 500 },
    );
  });

  it('returns a string containing the year for valid ISO dates', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 2000, max: 2099 }),
        fc.integer({ min: 1, max: 12 }),
        fc.integer({ min: 1, max: 28 }),
        (year, month, day) => {
          const iso = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}T12:00:00Z`;
          const result = formatDate(iso, 'en-US');
          expect(result).toContain(String(year));
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('formatDateTime properties', () => {
  it('never throws for any string input', () => {
    fc.assert(
      fc.property(fc.string(), (input) => {
        const result = formatDateTime(input, 'en-US');
        expect(typeof result).toBe('string');
      }),
      { numRuns: 500 },
    );
  });

  it('produces output at least as long as formatDate for valid dates', () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 2000, max: 2099 }),
        fc.integer({ min: 1, max: 12 }),
        fc.integer({ min: 1, max: 28 }),
        fc.integer({ min: 0, max: 23 }),
        fc.integer({ min: 0, max: 59 }),
        (year, month, day, hour, minute) => {
          const iso = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}T${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}:00Z`;
          const dateOnly = formatDate(iso, 'en-US');
          const dateTime = formatDateTime(iso, 'en-US');
          expect(dateTime.length).toBeGreaterThanOrEqual(dateOnly.length);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('formatDateOnly properties', () => {
  it('never throws for any string input', () => {
    fc.assert(
      fc.property(fc.string(), (input) => {
        const result = formatDateOnly(input, 'en-US');
        expect(typeof result).toBe('string');
      }),
      { numRuns: 500 },
    );
  });

  it('returns the raw input for non-date strings', () => {
    fc.assert(
      fc.property(
        fc.string().filter((s) => Number.isNaN(new Date(`${s}T00:00`).getTime())),
        (input) => {
          expect(formatDateOnly(input, 'en-US')).toBe(input);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('formatEpoch properties', () => {
  it('never throws for any number input', () => {
    fc.assert(
      fc.property(fc.double({ noNaN: true }), (epoch) => {
        // Must not throw regardless of value.
        const result = formatEpoch(epoch, 'en-US');
        // Result is either null or a string.
        expect(result === null || typeof result === 'string').toBe(true);
      }),
      { numRuns: 500 },
    );
  });

  it('returns null for zero and falsy values', () => {
    expect(formatEpoch(0, 'en')).toBeNull();
    expect(formatEpoch(null, 'en')).toBeNull();
    expect(formatEpoch(undefined, 'en')).toBeNull();
  });

  it('returns a non-null string for positive epoch seconds in reasonable range', () => {
    fc.assert(
      fc.property(
        // Unix timestamps from year 2000 to 2099.
        fc.integer({ min: 946684800, max: 4102444800 }),
        (epoch) => {
          const result = formatEpoch(epoch, 'en-US');
          expect(result).not.toBeNull();
          expect(typeof result).toBe('string');
        },
      ),
      { numRuns: 200 },
    );
  });

  it('formatEpoch and formatEpochDateTime agree on nullity', () => {
    fc.assert(
      fc.property(
        fc.oneof(
          fc.constant(null),
          fc.constant(undefined),
          fc.constant(0),
          fc.integer({ min: 1, max: 4102444800 }),
          fc.constant('not-a-number'),
        ),
        (input) => {
          const epoch = formatEpoch(input, 'en-US');
          const epochDt = formatEpochDateTime(input, 'en-US');
          // Both should be null or both non-null.
          expect(epoch === null).toBe(epochDt === null);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('formatDateNullable properties', () => {
  it('returns null for null, undefined, and zero time', () => {
    expect(formatDateNullable(null, 'en')).toBeNull();
    expect(formatDateNullable(undefined, 'en')).toBeNull();
    expect(formatDateNullable('0001-01-01T00:00:00Z', 'en')).toBeNull();
  });

  it('returns null or a string, never throws', () => {
    fc.assert(
      fc.property(fc.oneof(fc.constant(null), fc.constant(undefined), fc.string()), (input) => {
        const result = formatDateNullable(input, 'en-US');
        expect(result === null || typeof result === 'string').toBe(true);
      }),
      { numRuns: 500 },
    );
  });
});

describe('isZeroTime properties', () => {
  it('returns true for all dates before year 2000', () => {
    fc.assert(
      fc.property(fc.integer({ min: 1, max: 1999 }), (year) => {
        const iso = `${String(year).padStart(4, '0')}-06-15T12:00:00Z`;
        expect(isZeroTime(iso)).toBe(true);
      }),
      { numRuns: 200 },
    );
  });

  it('returns false for dates from year 2000 onward', () => {
    fc.assert(
      fc.property(fc.integer({ min: 2000, max: 9999 }), (year) => {
        const iso = `${year}-06-15T12:00:00Z`;
        expect(isZeroTime(iso)).toBe(false);
      }),
      { numRuns: 200 },
    );
  });

  it('returns true for non-parseable strings', () => {
    fc.assert(
      fc.property(
        fc.string().filter((s) => Number.isNaN(new Date(s).getTime())),
        (input) => {
          expect(isZeroTime(input)).toBe(true);
        },
      ),
      { numRuns: 200 },
    );
  });
});

describe('isOverdue properties', () => {
  it('returns false for null and undefined', () => {
    expect(isOverdue(null)).toBe(false);
    expect(isOverdue(undefined)).toBe(false);
  });

  it('returns false for far-future dates', () => {
    fc.assert(
      fc.property(fc.integer({ min: 2090, max: 2099 }), (year) => {
        expect(isOverdue(`${year}-12-31`)).toBe(false);
      }),
      { numRuns: 50 },
    );
  });

  it('returns true for dates well in the past', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-04-19T12:00:00'));

    fc.assert(
      fc.property(
        fc.integer({ min: 2000, max: 2025 }),
        fc.integer({ min: 1, max: 12 }),
        fc.integer({ min: 1, max: 28 }),
        (year, month, day) => {
          const dueOn = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
          expect(isOverdue(dueOn)).toBe(true);
        },
      ),
      { numRuns: 200 },
    );

    vi.useRealTimers();
  });
});
