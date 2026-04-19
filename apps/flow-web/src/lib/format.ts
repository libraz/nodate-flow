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
 * @param iso - ISO datetime string.
 * @returns `true` when the value is the Go zero time sentinel or has a year before 2000.
 */
export function isZeroTime(iso: string): boolean {
  if (iso === '0001-01-01T00:00:00Z') return true;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) || d.getFullYear() < 2000;
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

export function isOverdue(dueOn: string | null | undefined): boolean {
  if (!dueOn) return false;
  const now = new Date();
  const todayKey = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  return dueOn < todayKey;
}
