/**
 * The week-start preference reaching the date pickers.
 *
 * The `/calendar` grid honoured `me.weekStart` including Saturday; every
 * DatePicker in the product laid out a Monday week regardless, because
 * the primitive's prop could only say `sunday` or `monday` and nobody
 * passed it. An account that chose Saturday saw two different weeks
 * depending on which surface it was looking at, and could not express its
 * setting in the picker at all.
 */

import { describe, expect, it } from 'vitest';

import { toWeekStartDay } from '../use-week-start';

describe('toWeekStartDay', () => {
  it('maps every value the server can store', () => {
    expect(toWeekStartDay('mon')).toBe('monday');
    expect(toWeekStartDay('sun')).toBe('sunday');
    // The third value, which a monday/sunday flag cannot express.
    expect(toWeekStartDay('sat')).toBe('saturday');
  });

  it('falls back to the picker default when the session has no preference', () => {
    // Matches DatePicker's own default, so an account without a stored
    // value renders exactly as the picker does on its own.
    expect(toWeekStartDay(undefined)).toBe('monday');
    expect(toWeekStartDay('')).toBe('monday');
  });

  it('does not invent a value from an unrecognised input', () => {
    expect(toWeekStartDay('tuesday')).toBe('monday');
  });
});
