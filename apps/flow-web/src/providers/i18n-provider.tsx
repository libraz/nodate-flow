/**
 * App-specific I18nProvider that initializes the local i18n instance
 * and delegates to the shared provider from @nodate-flow/sdk.
 */
import type { ReactElement, ReactNode } from 'react';

import { I18nProvider as SharedI18nProvider } from '@nodate-flow/sdk';
import { setConfirmActionLabels } from '@nodate-flow/ui/primitives/confirm/action';

import { i18n, initI18n } from '../i18n';

initI18n();

setConfirmActionLabels(() => {
  const t = i18n.getFixedT(null, 'common');
  return {
    title: t('common.confirm_title'),
    confirmLabel: t('common.confirm'),
    cancelLabel: t('common.cancel'),
  };
});

/** Suspense-safe i18n provider. */
export function I18nProvider({ children }: { children: ReactNode }): ReactElement {
  return <SharedI18nProvider i18n={i18n}>{children}</SharedI18nProvider>;
}
