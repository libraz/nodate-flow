/**
 * EventDialog — recurrence custom placeholder i18n contract.
 *
 * The custom-RRULE textarea used to render the literal
 * `placeholder="RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR"` inline. Even though
 * that placeholder is developer-facing, leaking a hard-coded literal
 * breaks the namespace consistency rule and prevents translators from
 * adapting the example to locale conventions.
 *
 * Two assertions:
 *
 * 1. The placeholder string is now sourced from
 *    `t('placeholder.recurrenceCustom')` in the calendar-events ns.
 * 2. The key resolves in every locale (`en`, `ja`, `zh`).
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const LOCALES = ['en', 'ja', 'zh'] as const;
type Locale = (typeof LOCALES)[number];

interface CalendarEventsLocale {
  placeholder?: { recurrenceCustom?: string };
}

function readCalendarEvents(locale: Locale): CalendarEventsLocale {
  const p = resolve(__dirname, '..', '..', '..', '..', 'locales', locale, 'calendar-events.json');
  return JSON.parse(readFileSync(p, 'utf-8')) as CalendarEventsLocale;
}

describe('event-dialog recurrence-custom placeholder', () => {
  it('every locale defines placeholder.recurrenceCustom', () => {
    for (const locale of LOCALES) {
      const json = readCalendarEvents(locale);
      const value = json.placeholder?.recurrenceCustom;
      expect(
        typeof value,
        `${locale}/calendar-events.json missing placeholder.recurrenceCustom`,
      ).toBe('string');
      expect((value ?? '').length).toBeGreaterThan(0);
    }
  });

  it('event-dialog.tsx routes the recurrence placeholder through t()', () => {
    const source = readFileSync(resolve(__dirname, '..', 'event-dialog.tsx'), 'utf-8');
    expect(source).toContain("t('placeholder.recurrenceCustom')");
    expect(source).not.toContain('placeholder="RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR"');
  });
});
