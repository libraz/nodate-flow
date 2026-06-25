import { mergeCommon } from '@nodate-flow/i18n-shared';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import { initReactI18next } from 'react-i18next';

import enAdmin from '../../locales/en/admin.json';
import enAuth from '../../locales/en/auth.json';
import enCommon from '../../locales/en/common.json';
import enErrors from '../../locales/en/errors.json';
import enInstanceStats from '../../locales/en/instanceStats.json';
import jaAdmin from '../../locales/ja/admin.json';
import jaAuth from '../../locales/ja/auth.json';
import jaCommon from '../../locales/ja/common.json';
import jaErrors from '../../locales/ja/errors.json';
import jaInstanceStats from '../../locales/ja/instanceStats.json';
import zhAdmin from '../../locales/zh/admin.json';
import zhAuth from '../../locales/zh/auth.json';
import zhCommon from '../../locales/zh/common.json';
import zhErrors from '../../locales/zh/errors.json';
import zhInstanceStats from '../../locales/zh/instanceStats.json';

/** Supported UI languages. */
export const supportedLanguages = ['en', 'ja', 'zh'] as const;

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
  const lower = nav.toLowerCase();
  if (lower.startsWith('ja')) return 'ja';
  if (lower.startsWith('zh')) return 'zh';
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
      ns: ['auth', 'admin', 'common', 'errors', 'instanceStats'],
      resources: {
        en: {
          auth: enAuth,
          admin: enAdmin,
          common: mergeCommon('en', enCommon),
          errors: enErrors,
          instanceStats: enInstanceStats,
        },
        ja: {
          auth: jaAuth,
          admin: jaAdmin,
          common: mergeCommon('ja', jaCommon),
          errors: jaErrors,
          instanceStats: jaInstanceStats,
        },
        zh: {
          auth: zhAuth,
          admin: zhAdmin,
          common: mergeCommon('zh', zhCommon),
          errors: zhErrors,
          instanceStats: zhInstanceStats,
        },
      },
      interpolation: { escapeValue: false },
      react: { useSuspense: true },
    });
  // Keep <html lang> in sync with the active i18n locale. The index.html boot
  // script sets it once at load from localStorage; this listener keeps it
  // accurate after in-session changes (profile form save, post-login bootstrap).
  i18n.on('languageChanged', (lng) => {
    if (typeof document !== 'undefined') {
      document.documentElement.lang = lng;
    }
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
