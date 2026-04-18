import Holidays from 'date-holidays';
import type { HolidayEntry, HolidayProvider, WeekendConfig } from '../types';

const VALID_TYPES = new Set<string>(['public', 'bank', 'observance', 'optional']);

const weekendMap = new Map<string, number[]>([
  ['AE', [5, 6]],
  ['SA', [5, 6]],
  ['IR', [5]],
]);

const displayNames = new Map<string, Record<string, string>>([
  ['JP', { ja: '日本の祝日', en: 'Japanese Holidays' }],
  ['US', { en: 'US Holidays', ja: 'アメリカの祝日' }],
  ['GB', { en: 'UK Holidays', ja: 'イギリスの祝日' }],
  ['DE', { en: 'German Holidays', ja: 'ドイツの祝日' }],
  ['FR', { en: 'French Holidays', ja: 'フランスの祝日' }],
  ['KR', { en: 'Korean Holidays', ja: '韓国の祝日', ko: '한국 공휴일' }],
  ['CN', { en: 'Chinese Holidays', ja: '中国の祝日', zh: '中国节假日' }],
]);

export class DateHolidaysProvider implements HolidayProvider {
  readonly code: string;
  private hd: Holidays;
  private weekendDays: number[];

  constructor(countryCode: string) {
    this.code = countryCode.toUpperCase();
    this.hd = new Holidays(this.code);
    this.weekendDays = weekendMap.get(this.code) ?? [0, 6];
  }

  displayName(locale: string): string {
    const countryNames = displayNames.get(this.code);
    if (countryNames) {
      const lang = locale.split('-')[0];
      return (
        countryNames[locale] ??
        countryNames[lang ?? ''] ??
        countryNames.en ??
        `${this.code} Holidays`
      );
    }
    return `${this.code} Holidays`;
  }

  holidays(year: number, locale?: string): HolidayEntry[] {
    const lang = locale?.split('-')[0];

    const raw = this.hd.getHolidays(year);

    let localizedMap: Map<string, string> | undefined;
    if (lang) {
      const hdLocale = new Holidays(this.code);
      hdLocale.setLanguages(lang);
      const localized = hdLocale.getHolidays(year);
      localizedMap = new Map(localized.map((h) => [h.date.slice(0, 10), h.name]));
    }

    const hdEn = new Holidays(this.code);
    hdEn.setLanguages('en');
    const enHolidays = hdEn.getHolidays(year);
    const enMap = new Map(enHolidays.map((h) => [h.date.slice(0, 10), h.name]));

    return raw
      .filter((h) => VALID_TYPES.has(h.type))
      .map((h) => {
        const dateStr = h.date.slice(0, 10);
        const localNames: Record<string, string> = {};

        const enName = enMap.get(dateStr);
        if (enName) localNames.en = enName;

        const defaultLangs = this.hd.getLanguages();
        if (defaultLangs[0] && defaultLangs[0] !== 'en') {
          localNames[defaultLangs[0]] = h.name;
        }

        if (lang && localizedMap) {
          const localizedName = localizedMap.get(dateStr);
          if (localizedName) localNames[lang] = localizedName;
        }

        const localizedName = lang ? localizedMap?.get(dateStr) : undefined;
        const name = localizedName ?? h.name;

        return {
          date: dateStr,
          name,
          localNames,
          type: h.type as HolidayEntry['type'],
        };
      });
  }

  holidaysBetween(start: Date, end: Date, locale?: string): HolidayEntry[] {
    const results: HolidayEntry[] = [];
    const startYear = start.getFullYear();
    const endYear = end.getFullYear();
    for (let y = startYear; y <= endYear; y++) {
      for (const h of this.holidays(y, locale)) {
        const d = new Date(`${h.date}T00:00:00`);
        if (d >= start && d < end) {
          results.push(h);
        }
      }
    }
    return results;
  }

  isHoliday(date: Date, locale?: string): HolidayEntry | null {
    const result = this.hd.isHoliday(date);
    if (!result || result.length === 0) return null;

    const h = result.find((r) => r.type === 'public' || r.type === 'bank');
    if (!h) return null;

    const lang = locale?.split('-')[0];
    const localNames: Record<string, string> = {};

    const hdEn = new Holidays(this.code);
    hdEn.setLanguages('en');
    const enResult = hdEn.isHoliday(date);
    if (enResult && enResult.length > 0 && enResult[0]) {
      localNames.en = enResult[0].name;
    }

    const defaultLangs = this.hd.getLanguages();
    if (defaultLangs[0] && defaultLangs[0] !== 'en') {
      localNames[defaultLangs[0]] = h.name;
    }

    if (lang && lang !== 'en') {
      const hdLocale = new Holidays(this.code);
      hdLocale.setLanguages(lang);
      const locResult = hdLocale.isHoliday(date);
      if (locResult && locResult.length > 0 && locResult[0]) {
        localNames[lang] = locResult[0].name;
      }
    }

    const name = lang && localNames[lang] ? localNames[lang] : h.name;

    return {
      date: date.toISOString().slice(0, 10),
      name,
      localNames,
      type: h.type as HolidayEntry['type'],
    };
  }

  weekendConfig(): WeekendConfig {
    return { days: this.weekendDays };
  }

  isWeekend(date: Date): boolean {
    return this.weekendDays.includes(date.getDay());
  }

  isNonWorkingDay(date: Date, locale?: string): boolean {
    return this.isWeekend(date) || this.isHoliday(date, locale) !== null;
  }
}
