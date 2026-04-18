export interface HolidayEntry {
  /** ISO date string "2026-01-01" */
  date: string;
  /** Display name in the requested locale */
  name: string;
  /** Localized names keyed by BCP 47 tag */
  localNames: Record<string, string>;
  /** Holiday classification */
  type: 'public' | 'bank' | 'observance' | 'optional';
}

export interface WeekendConfig {
  /** Day-of-week numbers (0=Sunday, 6=Saturday) */
  days: number[];
}

export interface HolidayProvider {
  /** Country/region code (e.g., "JP", "US") */
  readonly code: string;
  /** Human-readable name for the given locale */
  displayName(locale: string): string;
  /** All holidays for a given year */
  holidays(year: number, locale?: string): HolidayEntry[];
  /** Holidays within a date range [start, end) */
  holidaysBetween(start: Date, end: Date, locale?: string): HolidayEntry[];
  /** Check if a specific date is a holiday */
  isHoliday(date: Date, locale?: string): HolidayEntry | null;
  /** Weekend days for this country */
  weekendConfig(): WeekendConfig;
  /** Check if a date is a weekend */
  isWeekend(date: Date): boolean;
  /** Check if a date is non-working (holiday or weekend) */
  isNonWorkingDay(date: Date, locale?: string): boolean;
}
