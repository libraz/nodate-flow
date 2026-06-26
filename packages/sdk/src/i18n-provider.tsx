import type { i18n as I18nInstance } from 'i18next';
import { type ReactElement, type ReactNode, Suspense, useEffect } from 'react';
import { I18nextProvider } from 'react-i18next';

/** Props for the shared I18nProvider. Each app passes its own i18n instance. */
export interface I18nProviderProps {
  children: ReactNode;
  /** Pre-configured i18next instance from the consuming app. */
  i18n: I18nInstance;
}

function syncHtmlLang(lang: string): void {
  if (typeof document === 'undefined') return;
  const base = lang.split('-')[0] || lang;
  if (document.documentElement.getAttribute('lang') !== base) {
    document.documentElement.setAttribute('lang', base);
  }
}

/** Suspense-safe i18n provider. Mirrors the current i18n language onto `<html lang>`. */
export function I18nProvider({ children, i18n }: I18nProviderProps): ReactElement {
  useEffect(() => {
    syncHtmlLang(i18n.language);
    const handler = (lng: string): void => syncHtmlLang(lng);
    i18n.on('languageChanged', handler);
    return () => {
      i18n.off('languageChanged', handler);
    };
  }, [i18n]);

  return (
    <I18nextProvider i18n={i18n}>
      <Suspense fallback={null}>{children}</Suspense>
    </I18nextProvider>
  );
}
