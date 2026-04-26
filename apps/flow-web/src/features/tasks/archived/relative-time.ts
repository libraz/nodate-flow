/**
 * Locale-aware "X ago" formatter using `Intl.RelativeTimeFormat`.
 *
 * Picks the best unit for the magnitude of the delta (now − epoch),
 * always returning a past-tense phrase ("3 days ago", "1 hour ago").
 * Returns `null` when the input is falsy, NaN, or in the future — the
 * caller renders a placeholder dash in those cases.
 */

const UNIT_THRESHOLDS: Array<{ unit: Intl.RelativeTimeFormatUnit; seconds: number }> = [
  { unit: 'year', seconds: 60 * 60 * 24 * 365 },
  { unit: 'month', seconds: 60 * 60 * 24 * 30 },
  { unit: 'week', seconds: 60 * 60 * 24 * 7 },
  { unit: 'day', seconds: 60 * 60 * 24 },
  { unit: 'hour', seconds: 60 * 60 },
  { unit: 'minute', seconds: 60 },
];

/**
 * Format a unix-second epoch as "X ago" relative to `now` (default
 * `Date.now()`). The negative sign on the value is what flips the
 * formatter into the past tense across every locale.
 */
export function formatTimeAgo(
  epochSeconds: number | undefined,
  locale: string,
  now: number = Date.now(),
): string | null {
  if (!epochSeconds) return null;
  if (Number.isNaN(epochSeconds)) return null;
  const deltaSeconds = Math.round(now / 1000) - Math.floor(epochSeconds);
  if (deltaSeconds < 0) return null;
  try {
    const fmt = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' });
    for (const { unit, seconds } of UNIT_THRESHOLDS) {
      if (deltaSeconds >= seconds) {
        const value = Math.floor(deltaSeconds / seconds);
        return fmt.format(-value, unit);
      }
    }
    return fmt.format(-Math.max(1, deltaSeconds), 'second');
  } catch {
    return null;
  }
}
