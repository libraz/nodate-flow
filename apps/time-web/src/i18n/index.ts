import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';

import enCommon from '../locales/en/common.json';
import jaCommon from '../locales/ja/common.json';

export const supportedLanguages = ['en', 'ja'] as const;

export type SupportedLanguage = (typeof supportedLanguages)[number];

const languageStorageKey = 'nt.lang';

/** Initialize the singleton i18next instance. Safe to call multiple times. */
export function initI18n(): typeof i18n {
  if (i18n.isInitialized) return i18n;
  void i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      fallbackLng: 'en',
      supportedLngs: supportedLanguages as unknown as string[],
      defaultNS: 'common',
      ns: ['common'],
      resources: {
        en: { common: enCommon },
        ja: { common: jaCommon },
      },
      detection: {
        order: ['localStorage', 'navigator'],
        lookupLocalStorage: languageStorageKey,
        caches: ['localStorage'],
      },
      interpolation: { escapeValue: false },
      react: { useSuspense: true },
    });
  // Sync <html lang> with detected/configured language
  document.documentElement.lang = i18n.language;
  i18n.on('languageChanged', (lng) => {
    document.documentElement.lang = lng;
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
  document.documentElement.lang = lang;
  void i18n.changeLanguage(lang);
}

export { i18n };
