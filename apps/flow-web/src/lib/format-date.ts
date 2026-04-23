/**
 * Shared due-date formatting helpers.
 *
 * The web app stores due dates as naive calendar dates (`YYYY-MM-DD`) on
 * the API boundary. Feeding such a string directly into `new Date()` is
 * ambiguous: ECMAScript parses date-only ISO strings as UTC midnight,
 * which then shifts to the previous day in negative-offset timezones
 * (e.g. `2026-04-28` renders as `Apr 27, 2026` in PDT). These helpers
 * parse the components manually and build a local-timezone `Date` so the
 * user-visible day stays stable.
 */
const ISO_DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

/**
 * Parse a `YYYY-MM-DD` ISO date string as a local calendar date.
 *
 * @param iso - Date string in `YYYY-MM-DD` format.
 * @returns `Date` anchored at local midnight for the given day, or `null`
 * when the input does not match the expected pattern or components are
 * out of range.
 */
function parseIsoDateLocal(iso: string): Date | null {
  const match = ISO_DATE_RE.exec(iso);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  if (!Number.isFinite(year) || !Number.isFinite(month) || !Number.isFinite(day)) return null;
  const d = new Date(year, month - 1, day);
  return Number.isNaN(d.getTime()) ? null : d;
}

/**
 * Format an ISO date string (`YYYY-MM-DD`) for user-facing display.
 *
 * Parses the ISO string as a naive calendar date (no timezone shift) so
 * `2026-04-28` never slips to `2026-04-27` in Pacific timezones. Uses
 * `Intl.DateTimeFormat` with `{ dateStyle: 'medium' }` so the output
 * shape tracks the user's locale:
 * - `ja`: `2026/04/28`
 * - `en`: `Apr 28, 2026`
 *
 * Falls back to the raw input when the string cannot be parsed or the
 * formatter throws, so callers never have to guard against exceptions.
 *
 * @param iso - Due date string in `YYYY-MM-DD` format.
 * @param locale - BCP 47 language tag (e.g. `'en'`, `'ja'`).
 * @returns Locale-formatted due date string.
 */
export function formatDueDate(iso: string, locale: string): string {
  const d = parseIsoDateLocal(iso);
  if (!d) return iso;
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(d);
  } catch {
    return iso;
  }
}

/**
 * Same as `formatDueDate` but accepts `null`/`undefined` and returns an
 * empty string for nullish input. Convenience for list renderers that
 * show a fallback label (e.g. "no due date") via a separate branch.
 *
 * @param iso - Due date string in `YYYY-MM-DD` format, or a nullish
 * value when the task has no due date.
 * @param locale - BCP 47 language tag.
 * @returns Locale-formatted due date string, or `''` when `iso` is
 * nullish.
 */
export function formatDueDateOrEmpty(iso: string | null | undefined, locale: string): string {
  if (!iso) return '';
  return formatDueDate(iso, locale);
}
