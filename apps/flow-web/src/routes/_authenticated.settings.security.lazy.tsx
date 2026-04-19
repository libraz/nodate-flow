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
  padding: '1.25rem 1.5rem',
  borderRadius: '0.75rem',
  border: '1px solid var(--nf-color-border, var(--nf-color-border))',
  background: 'var(--nf-color-surface, var(--nf-color-surface))',
} as const;

const sectionHeadingStyle = {
  margin: 0,
  fontSize: '1rem',
  fontWeight: 600,
  color: 'var(--nf-color-fg)',
  paddingBlockEnd: '0.75rem',
  borderBlockEnd: '1px solid var(--nf-color-border, var(--nf-color-border))',
} as const;

function SecurityRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('security.title')}</h1>

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
