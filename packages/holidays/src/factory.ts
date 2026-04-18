import { DateHolidaysProvider } from './plugins/date-holidays-provider';
import { getProvider, registerProvider } from './registry';
import type { HolidayProvider } from './types';

export function getOrCreateProvider(countryCode: string): HolidayProvider {
  const code = countryCode.toUpperCase();
  let provider = getProvider(code);
  if (!provider) {
    provider = new DateHolidaysProvider(code);
    registerProvider(provider);
  }
  return provider;
}
