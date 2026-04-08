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
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem' }}>
      <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('integrations.title')}</h1>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            <Skeleton style={{ blockSize: '5rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '5rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '5rem', inlineSize: '100%' }} />
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
