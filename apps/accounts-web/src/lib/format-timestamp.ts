/**
 * Shared unix-timestamp -> localized string formatter for admin pages.
 *
 * Centralizes the formatter that was duplicated across five admin routes
 * (admins, audit-logs, users, users/$userId, workspaces, workspaces/$wsId).
 * The API-level convention is `*_at` = unixtime seconds (see
 * docs/conventions/api-types.md), so the helper multiplies by 1000 before
 * handing off to the platform `Intl` formatter via `toLocaleString`.
 *
 * @param ts        Unix timestamp in seconds, or `null` / `undefined` / `0`
 *                  to signal "never" (no recorded timestamp).
 * @param fallback  Localized "never" string rendered when `ts` is missing.
 *                  Optional; if omitted, an empty string is returned for
 *                  missing values.
 * @returns         Localized date-time string, or `fallback` when missing.
 */
export function formatTimestamp(ts: number | null | undefined, fallback = ''): string {
  if (ts === null || ts === undefined || ts === 0) return fallback;
  return new Date(ts * 1000).toLocaleString();
}
