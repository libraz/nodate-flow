import { describe, expect, test } from 'bun:test';
import { getOrCreateProvider } from '../factory';
import { DateHolidaysProvider } from '../plugins/date-holidays-provider';
import { getProvider, getRegisteredCodes } from '../registry';

describe('DateHolidaysProvider - Japan', () => {
  const provider = new DateHolidaysProvider('JP');

  test('2026 holidays include New Year (Jan 1)', () => {
    const holidays = provider.holidays(2026);
    const newYear = holidays.find((h) => h.date === '2026-01-01');
    expect(newYear).toBeDefined();
    expect(newYear?.type).toBe('public');
  });

  test('isHoliday returns entry for 2026-01-01', () => {
    const result = provider.isHoliday(new Date('2026-01-01T00:00:00'));
    expect(result).not.toBeNull();
    expect(result?.date).toBe('2026-01-01');
    expect(result?.type).toBe('public');
  });

  test('isHoliday returns null for a regular weekday', () => {
    const result = provider.isHoliday(new Date('2026-01-05T00:00:00'));
    expect(result).toBeNull();
  });

  test('holidaysBetween returns correct holidays for a month range', () => {
    const start = new Date('2026-01-01T00:00:00');
    const end = new Date('2026-02-01T00:00:00');
    const holidays = provider.holidaysBetween(start, end);
    expect(holidays.length).toBeGreaterThan(0);
    for (const h of holidays) {
      expect(h.date >= '2026-01-01').toBe(true);
      expect(h.date < '2026-02-01').toBe(true);
    }
  });

  test('isWeekend identifies Saturday and Sunday', () => {
    expect(provider.isWeekend(new Date('2026-01-03T00:00:00'))).toBe(true);
    expect(provider.isWeekend(new Date('2026-01-04T00:00:00'))).toBe(true);
    expect(provider.isWeekend(new Date('2026-01-05T00:00:00'))).toBe(false);
  });

  test('isNonWorkingDay returns true for both holidays and weekends', () => {
    expect(provider.isNonWorkingDay(new Date('2026-01-01T00:00:00'))).toBe(true);
    expect(provider.isNonWorkingDay(new Date('2026-01-03T00:00:00'))).toBe(true);
    expect(provider.isNonWorkingDay(new Date('2026-01-05T00:00:00'))).toBe(false);
  });

  test('displayName returns Japanese name for ja locale', () => {
    expect(provider.displayName('ja')).toBe('日本の祝日');
  });
});

describe('Registry auto-creation', () => {
  test('getOrCreateProvider lazily registers a provider', () => {
    const provider = getOrCreateProvider('US');
    expect(provider.code).toBe('US');
    expect(getProvider('US')).toBe(provider);
    expect(getRegisteredCodes()).toContain('US');
  });

  test('getOrCreateProvider returns same instance on repeated calls', () => {
    const first = getOrCreateProvider('DE');
    const second = getOrCreateProvider('DE');
    expect(first).toBe(second);
  });
});
