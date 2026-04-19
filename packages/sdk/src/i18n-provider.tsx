import { type ReactElement, type ReactNode, Suspense } from 'react';
import { I18nextProvider } from 'react-i18next';

import type { i18n as I18nInstance } from 'i18next';

/** Props for the shared I18nProvider. Each app passes its own i18n instance. */
export interface I18nProviderProps {
  children: ReactNode;
  /** Pre-configured i18next instance from the consuming app. */
  i18n: I18nInstance;
}

/** Suspense-safe i18n provider. */
export function I18nProvider({ children, i18n }: I18nProviderProps): ReactElement {
  return (
    <I18nextProvider i18n={i18n}>
      <Suspense fallback={null}>{children}</Suspense>
    </I18nextProvider>
  );
}
