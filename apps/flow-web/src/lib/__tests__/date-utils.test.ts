import { describe, expect, it } from 'vitest';

import { dateKey } from '../date-utils';

describe('dateKey', () => {
  it('formats a normal date as YYYY-MM-DD', () => {
    expect(dateKey(new Date(2024, 0, 15))).toBe('2024-01-15');
  });

  it('pads single-digit months with a leading zero', () => {
    expect(dateKey(new Date(2024, 2, 10))).toBe('2024-03-10');
  });

  it('pads single-digit days with a leading zero', () => {
    expect(dateKey(new Date(2024, 5, 5))).toBe('2024-06-05');
  });

  it('handles December 31 at the year boundary', () => {
    expect(dateKey(new Date(2024, 11, 31))).toBe('2024-12-31');
  });

  it('handles January 1 at the year boundary', () => {
    expect(dateKey(new Date(2025, 0, 1))).toBe('2025-01-01');
  });

  it('handles a leap year date (Feb 29)', () => {
    expect(dateKey(new Date(2024, 1, 29))).toBe('2024-02-29');
  });
});
