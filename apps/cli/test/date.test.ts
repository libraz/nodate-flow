/**
 * Unit tests for `assertYmd` — the client-side `YYYY-MM-DD` validator
 * used by `tnk task create`/`update` to reject malformed `--due` /
 * `--start` flag values before issuing an HTTP request.
 */

import { describe, expect, it } from 'vitest';

import { DateValidationError, assertYmd } from '../src/util/date.js';

describe('assertYmd', () => {
  describe('valid input', () => {
    it('returns the input verbatim when the date is a real day', () => {
      expect(assertYmd('2025-01-15', '--due')).toBe('2025-01-15');
    });

    it('accepts the first day of the year', () => {
      expect(assertYmd('2030-01-01', '--due')).toBe('2030-01-01');
    });

    it('accepts a leap day in a leap year', () => {
      expect(assertYmd('2024-02-29', '--due')).toBe('2024-02-29');
    });

    it('accepts the boundary day 31 in a 31-day month', () => {
      expect(assertYmd('2025-12-31', '--due')).toBe('2025-12-31');
    });
  });

  describe('invalid format', () => {
    it('rejects an empty string', () => {
      expect(() => assertYmd('', '--due')).toThrow(DateValidationError);
      expect(() => assertYmd('', '--due')).toThrow(/empty value/i);
    });

    it('rejects a string missing a separator', () => {
      expect(() => assertYmd('20250115', '--due')).toThrow(DateValidationError);
      expect(() => assertYmd('20250115', '--due')).toThrow(/YYYY-MM-DD format/);
    });

    it('rejects a string with the wrong length', () => {
      expect(() => assertYmd('2025-1-15', '--due')).toThrow(DateValidationError);
    });

    it('rejects non-digit characters', () => {
      expect(() => assertYmd('20a5-01-15', '--due')).toThrow(DateValidationError);
    });

    it('rejects an ISO datetime string', () => {
      expect(() => assertYmd('2025-01-15T00:00:00Z', '--due')).toThrow(DateValidationError);
    });

    it('mentions the failing flag in the error message', () => {
      try {
        assertYmd('nope', '--start');
        throw new Error('unreachable');
      } catch (err) {
        expect(err).toBeInstanceOf(DateValidationError);
        expect((err as DateValidationError).message).toContain('--start');
      }
    });
  });

  describe('invalid calendar dates', () => {
    it('rejects month 13', () => {
      expect(() => assertYmd('2030-13-01', '--due')).toThrow(DateValidationError);
      expect(() => assertYmd('2030-13-01', '--due')).toThrow(/invalid month/i);
    });

    it('rejects month 00', () => {
      expect(() => assertYmd('2030-00-01', '--due')).toThrow(DateValidationError);
      expect(() => assertYmd('2030-00-01', '--due')).toThrow(/invalid month/i);
    });

    it('rejects day 45', () => {
      expect(() => assertYmd('2030-01-45', '--due')).toThrow(DateValidationError);
    });

    it('rejects day 00', () => {
      expect(() => assertYmd('2030-01-00', '--due')).toThrow(DateValidationError);
    });

    it('rejects Feb 30 (non-existent calendar date that passes the shape check)', () => {
      expect(() => assertYmd('2025-02-30', '--due')).toThrow(DateValidationError);
      expect(() => assertYmd('2025-02-30', '--due')).toThrow(/not a real calendar date/i);
    });

    it('rejects Feb 29 in a non-leap year', () => {
      expect(() => assertYmd('2025-02-29', '--due')).toThrow(DateValidationError);
    });

    it('rejects April 31 (April has only 30 days)', () => {
      expect(() => assertYmd('2025-04-31', '--due')).toThrow(DateValidationError);
    });

    it('rejects 2030-13-45 from the issue description', () => {
      expect(() => assertYmd('2030-13-45', '--due')).toThrow(DateValidationError);
    });
  });
});
