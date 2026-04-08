import i18n from 'i18next';
import ICU from 'i18next-icu';
import { initReactI18next } from 'react-i18next';

import enCommon from '../../locales/en/common.json';
import enSettings from '../../locales/en/settings.json';
import jaCommon from '../../locales/ja/common.json';
import jaSettings from '../../locales/ja/settings.json';
import { defaultNamespace } from './namespaces';

/** Supported UI languages for Phase 1. */
export const supportedLanguages = ['en', 'ja'] as const;

/** Union of supported UI language codes. */
export type SupportedLanguage = (typeof supportedLanguages)[number];

const languageStorageKey = 'nf.lang';

function detectInitialLanguage(): SupportedLanguage {
  try {
    const stored = localStorage.getItem(languageStorageKey);
    if (stored && (supportedLanguages as readonly string[]).includes(stored)) {
      return stored as SupportedLanguage;
    }
  } catch {
    // ignore
  }
  const nav = typeof navigator !== 'undefined' ? navigator.language : 'en';
  if (nav.toLowerCase().startsWith('ja')) return 'ja';
  return 'en';
}

/** Initialize the singleton i18next instance. Safe to call multiple times. */
export function initI18n(): typeof i18n {
  if (i18n.isInitialized) return i18n;
  void i18n
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: detectInitialLanguage(),
      fallbackLng: 'en',
      supportedLngs: supportedLanguages as unknown as string[],
      defaultNS: defaultNamespace,
      ns: [defaultNamespace, 'settings'],
      resources: {
        en: { common: enCommon, settings: enSettings },
        ja: { common: jaCommon, settings: jaSettings },
      },
      interpolation: { escapeValue: false },
      react: { useSuspense: true },
    });
  return i18n;
}

/** Persist and apply a new UI language. */
export function setLanguage(lang: SupportedLanguage): void {
  try {
    localStorage.setItem(languageStorageKey, lang);
  } catch {
    // ignore
  }
  void i18n.changeLanguage(lang);
}

export { i18n };
