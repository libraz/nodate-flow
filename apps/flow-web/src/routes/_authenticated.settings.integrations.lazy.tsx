/**
 * /settings/integrations — personal OAuth connections (lazy).
 * Renders a Suspense boundary around the integrations catalog panel
 * and reads the ?connected=<provider> / ?integration_error=<reason>
 * query params set by the callback handler to show a one-shot toast.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createLazyFileRoute, useRouter } from '@tanstack/react-router';
import { type ReactElement, Suspense, useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import IntegrationsPanel from '../features/settings/integrations-panel';
import panelStyles from '../features/settings/integrations-panel.module.css';
import ui from '../features/settings/settings-ui.module.css';

function IntegrationsRoute(): ReactElement {
  const { t } = useTranslation('settings');
  const router = useRouter();

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const connected = params.get('connected');
    const errorReason = params.get('integration_error');
    if (connected) {
      toaster.show({
        tone: 'success',
        message: t('integrations.connected_toast', {
          provider: t(`integrations.provider.${connected}.name`, { defaultValue: connected }),
        }),
      });
    }
    if (errorReason) {
      toaster.show({
        tone: 'danger',
        message: t('integrations.errors.callback_failed', { reason: errorReason }),
      });
    }
    if (connected || errorReason) {
      // Strip the query params so a refresh does not re-toast.
      const url = new URL(window.location.href);
      url.searchParams.delete('connected');
      url.searchParams.delete('integration_error');
      window.history.replaceState(null, '', url.toString());
      void router.invalidate();
    }
  }, [router, t]);

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <h1 className={ui.title}>{t('integrations.title')}</h1>
      </header>
      <Suspense
        fallback={
          <div className={ui.skeletonStack} aria-hidden="true">
            <Skeleton className={panelStyles.skeletonRow} />
            <Skeleton className={panelStyles.skeletonRow} />
            <Skeleton className={panelStyles.skeletonRow} />
          </div>
        }
      >
        <IntegrationsPanel />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings/integrations')({
  component: IntegrationsRoute,
});
