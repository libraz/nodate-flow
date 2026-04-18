import type { HolidayProvider } from './types';

const providers = new Map<string, HolidayProvider>();

export function registerProvider(provider: HolidayProvider): void {
  providers.set(provider.code, provider);
}

export function getProvider(code: string): HolidayProvider | undefined {
  return providers.get(code.toUpperCase());
}

export function getAllProviders(): HolidayProvider[] {
  return [...providers.values()];
}

export function getRegisteredCodes(): string[] {
  return [...providers.keys()];
}
