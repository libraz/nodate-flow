/**
 * Unit tests for the format utility functions.
 *
 * These are pure functions with no provider dependencies, so they
 * use plain Vitest without renderWithProviders.
 */

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

describe('formatDate', () => {
  it('formats an ISO datetime string to a medium date', () => {
    const result = formatDate('2026-04-19T10:00:00Z', 'en-US');
    expect(result).toContain('2026');
    expect(result).toContain('19');
  });

  it('returns the raw input when parsing fails', () => {
    expect(formatDate('not-a-date', 'en')).toBe('not-a-date');
  });
});

describe('formatDateTime', () => {
  it('includes both date and time components', () => {
    const result = formatDateTime('2026-04-19T14:30:00Z', 'en-US');
    expect(result).toContain('2026');
    // Time component should be present (format varies by timezone).
    expect(result.length).toBeGreaterThan(10);
  });

  it('returns raw input on failure', () => {
    expect(formatDateTime('bad', 'en')).toBe('bad');
  });
});

describe('formatDateOnly', () => {
  it('formats a YYYY-MM-DD string without timezone shift', () => {
    const result = formatDateOnly('2026-04-19', 'en-US');
    expect(result).toContain('2026');
    expect(result).toContain('19');
  });

  it('returns raw input on failure', () => {
    expect(formatDateOnly('nope', 'en')).toBe('nope');
  });
});

describe('formatDateNullable', () => {
  it('returns null for null input', () => {
    expect(formatDateNullable(null, 'en')).toBeNull();
  });

  it('returns null for undefined input', () => {
    expect(formatDateNullable(undefined, 'en')).toBeNull();
  });

  it('returns null for Go zero time', () => {
    expect(formatDateNullable('0001-01-01T00:00:00Z', 'en')).toBeNull();
  });

  it('formats a valid date', () => {
    const result = formatDateNullable('2026-04-19T10:00:00Z', 'en-US');
    expect(result).not.toBeNull();
    expect(result).toContain('2026');
  });
});

describe('isZeroTime', () => {
  it('returns true for Go zero time sentinel', () => {
    expect(isZeroTime('0001-01-01T00:00:00Z')).toBe(true);
  });

  it('returns true for dates before year 2000', () => {
    expect(isZeroTime('1999-12-31T23:59:59Z')).toBe(true);
  });

  it('returns false for a normal date', () => {
    expect(isZeroTime('2026-04-19T10:00:00Z')).toBe(false);
  });

  it('returns true for invalid date strings', () => {
    expect(isZeroTime('not-a-date')).toBe(true);
  });
});

describe('isOverdue', () => {
  it('returns false for null', () => {
    expect(isOverdue(null)).toBe(false);
  });

  it('returns false for undefined', () => {
    expect(isOverdue(undefined)).toBe(false);
  });

  it('returns true for a past date', () => {
    expect(isOverdue('2020-01-01')).toBe(true);
  });

  it('returns false for a far future date', () => {
    expect(isOverdue('2099-12-31')).toBe(false);
  });

  it('returns false for today', () => {
    // Use a fixed clock to avoid flakiness.
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-04-19T12:00:00'));

    // Today's date string should NOT be overdue (strictly before today).
    expect(isOverdue('2026-04-19')).toBe(false);

    vi.useRealTimers();
  });

  it('returns true for yesterday when clock is fixed', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-04-19T12:00:00'));

    expect(isOverdue('2026-04-18')).toBe(true);

    vi.useRealTimers();
  });
});

describe('formatEpoch', () => {
  it('formats a unix-second timestamp', () => {
    // 2026-04-19 00:00:00 UTC = 1776556800
    const result = formatEpoch(1776556800, 'en-US');
    expect(result).not.toBeNull();
    expect(result).toContain('2026');
  });

  it('returns null for zero', () => {
    expect(formatEpoch(0, 'en')).toBeNull();
  });

  it('returns null for null', () => {
    expect(formatEpoch(null, 'en')).toBeNull();
  });

  it('handles string epoch values', () => {
    const result = formatEpoch('1776556800', 'en-US');
    expect(result).not.toBeNull();
    expect(result).toContain('2026');
  });

  it('returns null for NaN string', () => {
    expect(formatEpoch('not-a-number', 'en')).toBeNull();
  });
});

describe('formatEpochDateTime', () => {
  it('formats a unix-second timestamp with time', () => {
    const result = formatEpochDateTime(1776556800, 'en-US');
    expect(result).not.toBeNull();
    expect(result).toContain('2026');
    // Should be longer than date-only because it includes time.
    const dateOnly = formatEpoch(1776556800, 'en-US');
    expect(result?.length).toBeGreaterThanOrEqual(dateOnly?.length ?? 0);
  });

  it('returns null for falsy input', () => {
    expect(formatEpochDateTime(null, 'en')).toBeNull();
    expect(formatEpochDateTime(undefined, 'en')).toBeNull();
    expect(formatEpochDateTime(0, 'en')).toBeNull();
  });
});
