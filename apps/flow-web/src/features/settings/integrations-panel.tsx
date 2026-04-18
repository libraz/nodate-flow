/**
 * IntegrationsPanel — /settings/integrations. Renders one card per
 * supported provider showing connect/disconnect state. Suspense mode.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import {
  type IntegrationProviderName,
  type ProviderStatus,
  useConnectIntegration,
  useDisconnectIntegration,
  useIntegrationsQuery,
} from './integrations-api';

const PROVIDER_ORDER: readonly IntegrationProviderName[] = ['github', 'slack', 'google_calendar'];

export default function IntegrationsPanel(): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { data: providers } = useIntegrationsQuery();
  const connect = useConnectIntegration();
  const disconnect = useDisconnectIntegration();

  const byName = new Map<string, ProviderStatus>();
  for (const p of providers) byName.set(p.provider, p);

  const handleConnect = async (name: IntegrationProviderName): Promise<void> => {
    try {
      const { authorizeUrl } = await connect.mutateAsync({
        provider: name,
        redirectTo: `${window.location.origin}/settings/integrations`,
      });
      try {
        const url = new URL(authorizeUrl);
        if (url.protocol !== 'https:' && url.protocol !== 'http:') {
          throw new Error('Invalid URL scheme');
        }
        window.location.assign(url.href);
      } catch {
        toaster.show({ tone: 'danger', message: t('integrations.errors.connect_failed') });
        return;
      }
    } catch {
      toaster.show({ tone: 'danger', message: t('integrations.errors.connect_failed') });
    }
  };

  const handleDisconnect = async (id: string, providerName: string): Promise<void> => {
    if (
      !(await confirmAction({
        message: t('integrations.disconnect_confirm', { provider: providerName }),
      }))
    )
      return;
    try {
      await disconnect.mutateAsync(id);
      toaster.show({ tone: 'success', message: t('integrations.disconnected') });
    } catch {
      toaster.show({ tone: 'danger', message: t('integrations.errors.disconnect_failed') });
    }
  };

  return (
    <section
      style={{ display: 'flex', flexDirection: 'column', gap: '1rem', maxInlineSize: '48rem' }}
    >
      <p style={{ margin: 0, color: 'var(--color-fg-muted)' }}>{t('integrations.description')}</p>
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.75rem',
        }}
      >
        {PROVIDER_ORDER.map((name) => {
          const p = byName.get(name);
          const configured = p?.configured ?? false;
          const connection = p?.connection ?? null;
          return (
            <li
              key={name}
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr auto',
                gap: '0.75rem',
                alignItems: 'center',
                padding: '1rem 1.25rem',
                border: '1px solid var(--color-border)',
                borderRadius: '0.5rem',
              }}
            >
              <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                <span style={{ fontWeight: 500, color: 'var(--color-fg)' }}>
                  {t(`integrations.provider.${name}.name`)}
                </span>
                <span style={{ fontSize: '0.875rem', color: 'var(--color-fg-muted)' }}>
                  {t(`integrations.provider.${name}.description`)}
                </span>
                {connection != null ? (
                  <span style={{ fontSize: '0.75rem', color: 'var(--color-fg-muted)' }}>
                    {t('integrations.connected_as', {
                      account: connection.externalAccountLabel,
                      time: new Date(connection.connectedAt * 1000).toLocaleString(i18n.language),
                    })}
                  </span>
                ) : !configured ? (
                  <span style={{ fontSize: '0.75rem', color: 'var(--color-fg-muted)' }}>
                    {t('integrations.not_configured')}
                  </span>
                ) : null}
              </div>
              <div>
                {connection != null ? (
                  <Button
                    type="button"
                    variant="danger"
                    disabled={disconnect.isPending}
                    onClick={() => {
                      void handleDisconnect(connection.id, t(`integrations.provider.${name}.name`));
                    }}
                  >
                    {t('integrations.disconnect')}
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="primary"
                    disabled={!configured || connect.isPending}
                    onClick={() => {
                      void handleConnect(name);
                    }}
                  >
                    {t('integrations.connect')}
                  </Button>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
