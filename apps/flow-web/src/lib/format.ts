/**
 * Shared date formatting and date comparison utilities.
 *
 * All formatting uses `Intl.DateTimeFormat` to respect the user's locale.
 * No locale-specific formatting is hand-written.
 */

/**
 * Format an ISO 8601 datetime or date string as a medium-length localised date.
 *
 * @example formatDate('2026-04-19T10:00:00Z', 'en-US') // 'Apr 19, 2026'
 * @param iso - ISO datetime string or YYYY-MM-DD date string.
 * @param locale - BCP 47 language tag (e.g. `'en'`, `'ja'`).
 * @returns Formatted date string, or the raw input when parsing fails.
 */
export function formatDate(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return iso;
  }
}

/**
 * Format an ISO 8601 datetime string as a medium-length localised date
 * including a short time component.
 *
 * @example formatDateTime('2026-04-19T10:30:00Z', 'en-US') // 'Apr 19, 2026, 10:30 AM'
 * @param iso - ISO datetime string.
 * @param locale - BCP 47 language tag.
 * @returns Formatted date+time string, or the raw input when parsing fails.
 */
export function formatDateTime(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(iso),
    );
  } catch {
    return iso;
  }
}

/**
 * Format a date-only string (YYYY-MM-DD) as a medium-length localised date.
 *
 * Appends `T00:00` before parsing to avoid timezone-shift issues that occur
 * when `new Date('YYYY-MM-DD')` is interpreted as UTC midnight.
 *
 * @example formatDateOnly('2026-04-19', 'en-US') // 'Apr 19, 2026'
 * @param dateStr - Date string in YYYY-MM-DD format.
 * @param locale - BCP 47 language tag.
 * @returns Formatted date string, or the raw input when parsing fails.
 */
export function formatDateOnly(dateStr: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(
      new Date(`${dateStr}T00:00`),
    );
  } catch {
    return dateStr;
  }
}

/**
 * Format an ISO / date string or nullable value, returning `null` for
 * zero-time sentinels (`0001-01-01T00:00:00Z`) or invalid dates.
 *
 * Useful for optional timestamp columns that may contain Go zero values.
 *
 * @param iso - ISO datetime string, or `null`/`undefined`.
 * @param locale - BCP 47 language tag.
 * @returns Formatted date string, or `null` when the input is absent/invalid.
 */
export function formatDateNullable(iso: string | null | undefined, locale: string): string | null {
  if (!iso || isZeroTime(iso)) return null;
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return null;
  }
}

/**
 * Check whether a date string represents a Go zero time or an invalid date.
 *
 * The "year before 2000" cutoff is evaluated in UTC so the result is
 * stable across timezones. Using `getFullYear()` would otherwise flip
 * `1999-12-31T23:59:59Z` to "year 2000" in any timezone east of UTC,
 * causing the function to return `false` and tests to fail
 * non-deterministically depending on where they run.
 *
 * @param iso - ISO datetime string.
 * @returns `true` when the value is the Go zero time sentinel or has a UTC year before 2000.
 */
export function isZeroTime(iso: string): boolean {
  if (iso === '0001-01-01T00:00:00Z') return true;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000;
}

/**
 * Check whether a due date (YYYY-MM-DD) is strictly before today in local time.
 *
 * @param dueOn - Date string in YYYY-MM-DD format, or `null`/`undefined`.
 * @returns `true` when the due date is in the past.
 */
/**
 * Format a unix-second timestamp as a medium-length localised date.
 *
 * @param epochSec - Unix timestamp in seconds.
 * @param locale - BCP 47 language tag.
 * @returns Formatted date string, or `null` for zero/falsy values.
 */
export function formatEpoch(
  epochSec: number | string | null | undefined,
  locale: string,
): string | null {
  if (!epochSec) return null;
  const n = typeof epochSec === 'string' ? Number(epochSec) : epochSec;
  if (Number.isNaN(n)) return null;
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(n * 1000));
  } catch {
    return null;
  }
}

/**
 * Format a unix-second timestamp as a medium-length localised date+time.
 *
 * @param epochSec - Unix timestamp in seconds.
 * @param locale - BCP 47 language tag.
 * @returns Formatted date+time string, or `null` for zero/falsy values.
 */
export function formatEpochDateTime(
  epochSec: number | string | null | undefined,
  locale: string,
): string | null {
  if (!epochSec) return null;
  const n = typeof epochSec === 'string' ? Number(epochSec) : epochSec;
  if (Number.isNaN(n)) return null;
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(n * 1000),
    );
  } catch {
    return null;
  }
}

/**
 * Format a numeric amount as a localised currency string via `Intl.NumberFormat`.
 *
 * Uses `currencyDisplay: 'narrowSymbol'` so non-home-currency locales still
 * render a concise sign (e.g. `$` for USD in ja) rather than `US$`.
 *
 * @example formatCurrency(0, 'USD', 'en') // '$0.00'
 * @example formatCurrency(12.5, 'USD', 'ja') // '$12.50'
 * @param amount - Numeric amount.
 * @param currency - ISO 4217 currency code (e.g. `'USD'`).
 * @param locale - BCP 47 language tag.
 * @returns Formatted currency string, or a plain fixed-decimal fallback when Intl throws.
 */
export function formatCurrency(amount: number, currency: string, locale: string): string {
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      currencyDisplay: 'narrowSymbol',
    }).format(amount);
  } catch {
    return amount.toFixed(2);
  }
}

export function isOverdue(dueOn: string | null | undefined): boolean {
  if (!dueOn) return false;
  const now = new Date();
  const todayKey = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  return dueOn < todayKey;
}
