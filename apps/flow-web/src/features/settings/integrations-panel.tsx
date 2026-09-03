/**
 * IntegrationsPanel — /settings/integrations. Renders one card per
 * supported provider showing connect/disconnect state. Suspense mode.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { getPublicBaseUrl } from '../../lib/public-base-url';
import {
  type IntegrationProviderName,
  type ProviderStatus,
  useConnectIntegration,
  useDisconnectIntegration,
  useIntegrationsQuery,
} from './integrations-api';
import styles from './integrations-panel.module.css';

const PROVIDER_ORDER: readonly IntegrationProviderName[] = [
  'github',
  'slack',
  'google_calendar',
  'discord',
];

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
        redirectTo: `${getPublicBaseUrl()}/settings/integrations`,
      });
      try {
        const url = new URL(authorizeUrl);
        if (url.protocol !== 'https:' && url.protocol !== 'http:') {
          throw new Error('Invalid URL scheme');
        }
        window.location.assign(url.href);
      } catch {
        // error-toast-exempt: this inner catch only sees the scheme rejection
        // thrown two lines up, whose message is a developer string, not
        // anything the API said about the authorize URL.
        toaster.show({ tone: 'danger', message: t('integrations.errors.connect_failed') });
        return;
      }
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'integrations.errors.connect_failed'),
      });
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
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'integrations.errors.disconnect_failed'),
      });
    }
  };

  return (
    <section className={styles.section}>
      <p className={styles.description}>{t('integrations.description')}</p>
      <ul className={styles.list}>
        {PROVIDER_ORDER.map((name) => {
          const p = byName.get(name);
          const configured = p?.configured ?? false;
          const connection = p?.connection ?? null;
          return (
            <li key={name} className={styles.row}>
              <div className={styles.identity}>
                <span className={styles.providerName}>
                  {t(`integrations.provider.${name}.name`)}
                </span>
                <span className={styles.providerDescription}>
                  {t(`integrations.provider.${name}.description`)}
                </span>
                {connection != null ? (
                  <span className={styles.metaSecondary}>
                    {t('integrations.connected_as', {
                      account: connection.externalAccountLabel,
                      time: new Date(connection.connectedAt * 1000).toLocaleString(i18n.language),
                    })}
                  </span>
                ) : !configured ? (
                  <span className={styles.metaSecondary}>{t('integrations.not_configured')}</span>
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
