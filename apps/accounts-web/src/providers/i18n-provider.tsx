/**
 * App-specific I18nProvider that initializes the local i18n instance
 * and delegates to the shared provider from @nodate-flow/sdk.
 */
import type { ReactElement, ReactNode } from 'react';

import { I18nProvider as SharedI18nProvider } from '@nodate-flow/sdk';

import { i18n, initI18n } from '../i18n';

initI18n();

/** Suspense-safe i18n provider. */
export function I18nProvider({ children }: { children: ReactNode }): ReactElement {
  return <SharedI18nProvider i18n={i18n}>{children}</SharedI18nProvider>;
}
