import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';

import { authResources } from './new-auth-keys';

export function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'auth',
      ns: ['auth'],
      resources: { en: { auth: authResources } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

export function withI18n(children: ReactNode): ReactElement {
  return <I18nextProvider i18n={buildI18n()}>{children}</I18nextProvider>;
}
