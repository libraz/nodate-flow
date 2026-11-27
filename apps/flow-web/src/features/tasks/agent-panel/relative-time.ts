/** Past or future distance from now, as an `Intl.RelativeTimeFormat` string. */
export function formatRelativeTime(unixSec: number, locale: string, nowMs = Date.now()): string {
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' });
    const diff = unixSec - Math.floor(nowMs / 1000);
    const abs = Math.abs(diff);
    if (abs < 60) return rtf.format(diff, 'second');
    if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute');
    if (abs < 86_400) return rtf.format(Math.round(diff / 3600), 'hour');
    if (abs < 2_592_000) return rtf.format(Math.round(diff / 86_400), 'day');
    return rtf.format(Math.round(diff / 2_592_000), 'month');
  } catch {
    return new Date(unixSec * 1000).toISOString();
  }
}

/** Absolute local date and time, for `title` / `<time>` text. */
export function formatAbsoluteTime(unixSec: number, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(unixSec * 1000),
    );
  } catch {
    return new Date(unixSec * 1000).toISOString();
  }
}
