import type { RecurrenceRule } from '@nodate-flow/ui/calendar/types';

/** Recurrence preset identifiers stored in component state. */
/**
 * The repeat choices the dialog offers, plus `custom` — which is not
 * offered, only shown.
 *
 * The presets are lossy: a stored rule can say things none of them can
 * (an interval, a set of weekdays that is not the working week). Before,
 * that was handled by not showing the stored rule at all, so every event
 * opened reading "Does not repeat" whether or not it repeated, and the
 * only way to stop a daily standup was to delete the series. `custom`
 * lets the control tell the truth about a rule it cannot offer to
 * recreate, while still allowing the user to replace it or clear it.
 */
export type RecurrencePreset =
  | 'none'
  | 'daily'
  | 'weekdays'
  | 'weekly'
  | 'monthly'
  | 'yearly'
  | 'custom';

/**
 * Translate a recurrence preset to a recurrence-rule payload. `'none'`
 * returns null so the caller omits the field.
 *
 * The emitted shape is the canonical {@link RecurrenceRule} the
 * `chk_calendar_events_recurrence_shape` CHECK stores: `freq` tokens are
 * lowercase (`daily` / `weekly` / `monthly` / `yearly`) and `byDay` holds
 * the uppercase two-letter codes (`MO` / `TU` / …). These are API enum
 * values — not UI copy — so they stay literal. No preset carries an
 * interval, so none needs `wkst`, and the default week start (`MO`) is
 * the one written by omission.
 */
export function presetToRRule(preset: RecurrencePreset, _startDate: string): RecurrenceRule | null {
  switch (preset) {
    case 'none':
      return null;
    // `custom` describes a rule this control did not author and cannot
    // reproduce, so selecting it changes nothing: null here means the
    // caller leaves the stored rule alone.
    case 'custom':
      return null;
    case 'daily':
      return { freq: 'daily' };
    case 'weekdays':
      return { freq: 'weekly', byDay: ['MO', 'TU', 'WE', 'TH', 'FR'] };
    case 'weekly':
      return { freq: 'weekly' };
    case 'monthly':
      return { freq: 'monthly' };
    case 'yearly':
      return { freq: 'yearly' };
  }
}

/**
 * Map a stored rule back onto the control, or `custom` when no preset
 * reproduces it.
 *
 * Deliberately strict: a rule with an interval, a count, an until or a
 * byMonthDay is not "weekly" even if its freq says so, because choosing
 * `weekly` in the control would silently drop the rest of it. Reporting
 * `custom` and leaving the rule untouched is the honest answer.
 */
export function rruleToPreset(rule: RecurrenceRule | null | undefined): RecurrencePreset {
  if (!rule) return 'none';
  const plain =
    (rule.interval === undefined || rule.interval === 1) &&
    rule.count === undefined &&
    rule.until === undefined &&
    (rule.byMonthDay === undefined || rule.byMonthDay.length === 0);
  if (!plain) return 'custom';

  const byDay = rule.byDay ?? [];
  switch (rule.freq) {
    case 'daily':
      return byDay.length === 0 ? 'daily' : 'custom';
    case 'weekly': {
      if (byDay.length === 0) return 'weekly';
      const weekdays = ['MO', 'TU', 'WE', 'TH', 'FR'];
      const normalized = [...byDay].map((d) => d.toUpperCase()).sort();
      return normalized.join(',') === [...weekdays].sort().join(',') ? 'weekdays' : 'custom';
    }
    case 'monthly':
      return byDay.length === 0 ? 'monthly' : 'custom';
    case 'yearly':
      return byDay.length === 0 ? 'yearly' : 'custom';
    default:
      return 'custom';
  }
}

/**
 * The weekday a `YYYY-MM-DD` key falls on, named in `locale`.
 *
 * The weekly repeat option names the day it repeats on, and that day is
 * a property of the event's own start rather than a separate choice.
 * `Intl` answers it in whichever language is active, so there is no day
 * table to keep in step across the locale files and no key per weekday.
 * Read in UTC because the key is already a calendar day: giving it any
 * other zone lets an offset name the day before.
 */
export function weekdayName(dayKey: string, locale: string): string {
  const [year, month, day] = dayKey.split('-').map(Number);
  if (!year || !month || !day) return '';
  return new Intl.DateTimeFormat(locale, { weekday: 'long', timeZone: 'UTC' }).format(
    new Date(Date.UTC(year, month - 1, day)),
  );
}
