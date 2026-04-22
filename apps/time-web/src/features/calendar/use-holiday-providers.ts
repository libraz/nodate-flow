import { type HolidayProvider, getOrCreateProvider } from '@nodate-flow/holidays';
import { useMemo } from 'react';

import { useCalendarsQuery } from './api';

const HOLIDAY_SLUG_PREFIX = 'holidays.';

/**
 * Derives the list of ISO 3166-1 alpha-2 codes that the workspace is
 * subscribed to holiday feeds for, based on system calendars with a
 * systemSlug of the form `holidays.<cc>`.
 */
function systemSlugToCountry(slug: string): string | null {
  if (!slug.startsWith(HOLIDAY_SLUG_PREFIX)) return null;
  const cc = slug.slice(HOLIDAY_SLUG_PREFIX.length).toUpperCase();
  return /^[A-Z]{2}$/.test(cc) ? cc : null;
}

export interface AggregateHolidayProvider {
  /** Returns the first matching holiday across all subscribed countries. */
  isHoliday: (date: Date, locale?: string) => ReturnType<HolidayProvider['isHoliday']>;
  /** Providers keyed by ISO 3166-1 alpha-2, in subscription order. */
  providers: Array<{ country: string; provider: HolidayProvider }>;
}

/**
 * Reads the workspace's subscribed holiday feeds (system calendars) and
 * returns an aggregate provider that queries each in order. Locale-aware
 * display names come from each underlying provider.
 */
export function useHolidayProviders(): AggregateHolidayProvider {
  const { data: calendars } = useCalendarsQuery();
  return useMemo(() => {
    const countries = new Set<string>();
    for (const cal of calendars ?? []) {
      if (cal.kind !== 'system' || !cal.systemSlug) continue;
      const cc = systemSlugToCountry(cal.systemSlug);
      if (cc) countries.add(cc);
    }
    const providers = Array.from(countries).map((country) => ({
      country,
      provider: getOrCreateProvider(country),
    }));
    return {
      providers,
      isHoliday: (date, locale) => {
        for (const entry of providers) {
          const hit = entry.provider.isHoliday(date, locale);
          if (hit) return hit;
        }
        return null;
      },
    };
  }, [calendars]);
}
