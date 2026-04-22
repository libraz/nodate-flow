import i18n from 'i18next';
import ICU from 'i18next-icu';
import { initReactI18next } from 'react-i18next';

import enAdmin from '../../locales/en/admin.json';
import enAuth from '../../locales/en/auth.json';
import enCommon from '../../locales/en/common.json';
import jaAdmin from '../../locales/ja/admin.json';
import jaAuth from '../../locales/ja/auth.json';
import jaCommon from '../../locales/ja/common.json';

/** Supported UI languages. */
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
      defaultNS: 'auth',
      ns: ['auth', 'admin', 'common'],
      resources: {
        en: {
          auth: enAuth,
          admin: enAdmin,
          common: enCommon,
        },
        ja: {
          auth: jaAuth,
          admin: jaAdmin,
          common: jaCommon,
        },
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
