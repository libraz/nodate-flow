/**
 * Unit tests for the shared `formatTimestamp` helper.
 *
 * The helper centralizes the unix-seconds -> localized-string conversion
 * that was previously copy-pasted across five admin routes. Behavior we
 * pin here:
 *   1. Real timestamps render via `Date.toLocaleString()` (i.e. produce
 *      a non-empty string that is *not* the fallback).
 *   2. `null` / `undefined` / `0` all map to the supplied fallback so
 *      callers can render `t('common.never')` cleanly.
 *   3. Omitting the fallback returns an empty string for missing values.
 */

import { describe, expect, it } from 'vitest';

import { formatTimestamp } from '../format-timestamp';

describe('formatTimestamp', () => {
  it('formats a real unix-seconds timestamp via toLocaleString', () => {
    // 2023-01-01T00:00:00Z is 1672531200 seconds past epoch.
    const out = formatTimestamp(1672531200, 'never');
    expect(out).not.toBe('never');
    expect(out.length).toBeGreaterThan(0);
    // toLocaleString output varies by host locale, so just sanity check
    // that it round-trips through Date.
    expect(out).toBe(new Date(1672531200 * 1000).toLocaleString());
  });

  it('returns the fallback for null', () => {
    expect(formatTimestamp(null, 'Never logged in')).toBe('Never logged in');
  });

  it('returns the fallback for undefined', () => {
    expect(formatTimestamp(undefined, 'Never logged in')).toBe('Never logged in');
  });

  it('returns the fallback for a zero timestamp', () => {
    // Many backends use 0 as a sentinel for "no timestamp recorded" rather
    // than NULL — we treat it the same as null.
    expect(formatTimestamp(0, 'never')).toBe('never');
  });

  it('returns an empty string by default when fallback is omitted', () => {
    expect(formatTimestamp(null)).toBe('');
    expect(formatTimestamp(undefined)).toBe('');
    expect(formatTimestamp(0)).toBe('');
  });

  it('does not collapse positive non-zero numbers into the fallback', () => {
    expect(formatTimestamp(1, 'never')).not.toBe('never');
  });
});
