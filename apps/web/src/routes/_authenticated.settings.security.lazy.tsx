/**
 * /settings/security — user security settings (lazy). Currently
 * shows the active sessions panel; password change and TOTP 2FA land
 * in follow-up PRs.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import PasswordPanel from '../features/settings/password-panel';
import SessionsPanel from '../features/settings/sessions-panel';
import TotpPanel from '../features/settings/totp-panel';

function SecurityRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '2rem' }}>
      <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('security.title')}</h1>

      <section style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <h2 style={{ margin: 0, fontSize: '1.125rem' }}>{t('security.password.title')}</h2>
        <PasswordPanel />
      </section>

      <section style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <h2 style={{ margin: 0, fontSize: '1.125rem' }}>{t('security.totp.title')}</h2>
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <TotpPanel />
        </Suspense>
      </section>

      <section style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <h2 style={{ margin: 0, fontSize: '1.125rem' }}>{t('security.sessions.title')}</h2>
        <Suspense
          fallback={
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
              <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
            </div>
          }
        >
          <SessionsPanel />
        </Suspense>
      </section>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/settings/security')({
  component: SecurityRoute,
});
