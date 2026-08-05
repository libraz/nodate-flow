/**
 * Unit tests for the shared `formatTimestamp` helper.
 *
 * The helper centralizes the unix-seconds -> localized-string conversion
 * that was previously copy-pasted across five admin routes. Behavior we
 * pin here:
 *   1. The output follows the locale the caller passes, not the host's.
 *      This is the whole point of the helper: it used to call
 *      `toLocaleString()` with no arguments, so the admin screens
 *      formatted to the browser while the product was set to another
 *      language.
 *   2. `null` / `undefined` / `0` all map to the supplied fallback so
 *      callers can render `t('common.never')` cleanly.
 *   3. Omitting the fallback returns an empty string for missing values.
 */

import { describe, expect, it } from 'vitest';

import { formatTimestamp } from '../format-timestamp';

// 2023-01-01T00:00:00Z.
const TS = 1672531200;

describe('formatTimestamp', () => {
  it('formats to the locale it is given', () => {
    const out = formatTimestamp(TS, { locale: 'ja', fallback: 'never' });
    expect(out).not.toBe('never');
    expect(out.length).toBeGreaterThan(0);
    expect(out).toBe(
      new Intl.DateTimeFormat('ja', { dateStyle: 'medium', timeStyle: 'short' }).format(
        new Date(TS * 1000),
      ),
    );
  });

  it('gives different locales different output', () => {
    const ja = formatTimestamp(TS, { locale: 'ja' });
    const en = formatTimestamp(TS, { locale: 'en-US' });
    expect(ja).not.toBe(en);
  });

  it('falls back to English rather than to the host when the tag is unusable', () => {
    const out = formatTimestamp(TS, { locale: 'not-a-locale' });
    expect(out).toBe(
      new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' }).format(
        new Date(TS * 1000),
      ),
    );
  });

  it('returns the fallback for null', () => {
    expect(formatTimestamp(null, { locale: 'en', fallback: 'Never logged in' })).toBe(
      'Never logged in',
    );
  });

  it('returns the fallback for undefined', () => {
    expect(formatTimestamp(undefined, { locale: 'en', fallback: 'Never logged in' })).toBe(
      'Never logged in',
    );
  });

  it('returns the fallback for a zero timestamp', () => {
    // Many backends use 0 as a sentinel for "no timestamp recorded" rather
    // than NULL — we treat it the same as null.
    expect(formatTimestamp(0, { locale: 'en', fallback: 'never' })).toBe('never');
  });

  it('returns an empty string by default when fallback is omitted', () => {
    expect(formatTimestamp(null, { locale: 'en' })).toBe('');
    expect(formatTimestamp(undefined, { locale: 'en' })).toBe('');
    expect(formatTimestamp(0, { locale: 'en' })).toBe('');
  });

  it('does not collapse positive non-zero numbers into the fallback', () => {
    expect(formatTimestamp(1, { locale: 'en', fallback: 'never' })).not.toBe('never');
  });
});
