/**
 * Relative-time formatting for the activity feed.
 *
 * Mirrors the timeline `EventCard.formatRelative` idiom verbatim: it builds
 * an `Intl.RelativeTimeFormat` from the active locale, clamps minor clock
 * skew so a just-now event never renders as "in 1 second", and steps down
 * second -> minute -> hour -> day -> month -> year buckets. `occurredAt`
 * is unix SECONDS.
 *
 * @param occurredAt - Event time in unix seconds.
 * @param locale - BCP 47 language tag (e.g. `'en'`, `'ja'`).
 * @returns A locale-aware relative phrase, or an ISO string on Intl failure.
 */
export function formatRelative(occurredAt: number, locale: string): string {
  try {
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
    const rawDiff = occurredAt - Math.floor(Date.now() / 1000);
    const diffSec = rawDiff > 0 ? 0 : rawDiff;
    const abs = Math.abs(diffSec);
    if (abs < 60) return rtf.format(Math.round(diffSec), 'second');
    if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
    if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour');
    if (abs < 2_592_000) return rtf.format(Math.round(diffSec / 86_400), 'day');
    if (abs < 31_536_000) return rtf.format(Math.round(diffSec / 2_592_000), 'month');
    return rtf.format(Math.round(diffSec / 31_536_000), 'year');
  } catch {
    return new Date(occurredAt * 1000).toISOString();
  }
}

/**
 * Absolute, locale-aware date+time for the row's `title`/`datetime`
 * tooltip. Returns an ISO string when Intl is unavailable.
 *
 * @param occurredAt - Event time in unix seconds.
 * @param locale - BCP 47 language tag.
 */
export function formatAbsolute(occurredAt: number, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(
      new Date(occurredAt * 1000),
    );
  } catch {
    return new Date(occurredAt * 1000).toISOString();
  }
}
