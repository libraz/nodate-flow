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

const sectionStyle = {
  display: 'flex',
  flexDirection: 'column',
  gap: '0.875rem',
  padding: 'var(--nf-space-5) var(--nf-space-6)',
  borderRadius: 'var(--nf-radius-lg)',
  border: '1px solid var(--nf-color-border)',
  background: 'var(--nf-color-surface)',
} as const;

const sectionHeadingStyle = {
  margin: 0,
  fontSize: 'var(--nf-text-base)',
  fontWeight: 600,
  color: 'var(--nf-color-fg)',
  paddingBlockEnd: 'var(--nf-space-3)',
  borderBlockEnd: '1px solid var(--nf-color-border)',
} as const;

function SecurityRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}>
      <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>{t('security.title')}</h1>

      <section style={sectionStyle}>
        <h2 style={sectionHeadingStyle}>{t('security.password.title')}</h2>
        <PasswordPanel />
      </section>

      <section style={sectionStyle}>
        <h2 style={sectionHeadingStyle}>{t('security.totp.title')}</h2>
        <Suspense fallback={<Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />}>
          <TotpPanel />
        </Suspense>
      </section>

      <section style={sectionStyle}>
        <h2 style={sectionHeadingStyle}>{t('security.sessions.title')}</h2>
        <Suspense
          fallback={
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
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
