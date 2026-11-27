import { describe, expect, it } from 'vitest';
import { occurrenceRange } from '../occurrence-range';

describe('occurrenceRange', () => {
  it('leaves a row that was not opened from an occurrence alone', () => {
    expect(occurrenceRange({ startAt: 100, endAt: 700 }, null)).toEqual({
      startAt: 100,
      endAt: 700,
    });
  });

  it('moves the stored span onto the occurrence that was opened', () => {
    expect(occurrenceRange({ startAt: 100, endAt: 700 }, 1_000)).toEqual({
      startAt: 1_000,
      endAt: 1_600,
    });
  });

  it('has nothing to place an end against when the row has no start', () => {
    expect(occurrenceRange({ endAt: 700 }, 1_000)).toEqual({ startAt: null, endAt: 700 });
  });

  it('leaves an absent end absent rather than inventing one', () => {
    expect(occurrenceRange({ startAt: 100 }, 1_000)).toEqual({ startAt: 1_000, endAt: null });
  });
});
