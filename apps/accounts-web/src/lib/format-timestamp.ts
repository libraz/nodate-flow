/**
 * Shared unix-timestamp -> localized string formatter for admin pages.
 *
 * Centralizes the formatter that was duplicated across five admin routes
 * (admins, audit-logs, users, users/$userId, workspaces, workspaces/$wsId).
 * The API-level convention is `*_at` = unixtime seconds (see
 * docs/conventions/api-types.md), so the helper multiplies by 1000 before
 * handing off to the platform `Intl` formatter.
 *
 * This used to call `toLocaleString()` with no arguments, which formats
 * to the *browser's* locale — a reader who set the product to Japanese
 * still got US-shaped timestamps on every admin screen, while
 * `/security` two routes away was correct. The language this product is
 * displayed in is chosen inside the product; nothing about the host OS
 * belongs in the output.
 *
 * The options come as an object rather than as more positional
 * arguments. A required `locale: string` in second position would have
 * accepted every existing `formatTimestamp(ts, t('common.never'))` call
 * unchanged — the fallback would have been read as the locale, thrown,
 * and silently formatted in English. An object cannot be mistaken for
 * the string that used to sit there, so the compiler names every call
 * site instead.
 *
 * @param ts       Unix timestamp in seconds, or `null` / `undefined` / `0`
 *                 to signal "never" (no recorded timestamp).
 * @param options  `locale` is a BCP 47 tag, normally
 *                 `i18n.resolvedLanguage`. `fallback` is the localized
 *                 "never" string rendered when `ts` is missing; omitted
 *                 means an empty string.
 * @returns        Localized date-time string, or the fallback when missing.
 */
export interface FormatTimestampOptions {
  locale: string;
  fallback?: string;
}

export function formatTimestamp(
  ts: number | null | undefined,
  options: FormatTimestampOptions,
): string {
  const { locale, fallback = '' } = options;
  if (ts === null || ts === undefined || ts === 0) return fallback;
  const date = new Date(ts * 1000);
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      date,
    );
  } catch {
    // An unknown tag throws RangeError. Falling back to the browser's
    // idea of a locale is the thing this function exists to prevent, so
    // fall back to the language we know the product always has.
    return new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' }).format(date);
  }
}
