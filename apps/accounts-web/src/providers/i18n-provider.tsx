import { type ReactElement, type ReactNode, Suspense } from 'react';
import { I18nextProvider } from 'react-i18next';

import { i18n, initI18n } from '../i18n';

initI18n();

/** Suspense-safe i18n provider. */
export function I18nProvider({ children }: { children: ReactNode }): ReactElement {
  return (
    <I18nextProvider i18n={i18n}>
      <Suspense fallback={null}>{children}</Suspense>
    </I18nextProvider>
  );
}
