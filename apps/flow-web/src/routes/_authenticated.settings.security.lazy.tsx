/**
 * /settings/security — user security settings (lazy): password change,
 * TOTP 2FA, and the active sessions list, one bordered section each.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import PasswordPanel from '../features/settings/password-panel';
import SessionsPanel from '../features/settings/sessions-panel';
import ui from '../features/settings/settings-ui.module.css';
import TotpPanel from '../features/settings/totp-panel';
import styles from './_authenticated.settings.security.module.css';

function SecurityRoute(): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <h1 className={ui.title}>{t('security.title')}</h1>
      </header>

      <section className={ui.section} aria-labelledby="security-password-title">
        <h2 id="security-password-title" className={ui.sectionTitle}>
          {t('security.password.title')}
        </h2>
        <PasswordPanel />
      </section>

      <section className={ui.section} aria-labelledby="security-totp-title">
        <h2 id="security-totp-title" className={ui.sectionTitle}>
          {t('security.totp.title')}
        </h2>
        <Suspense
          fallback={
            <div className={ui.skeletonStack} aria-hidden="true">
              <Skeleton className={styles.skeletonLine} />
              <Skeleton className={styles.skeletonField} />
            </div>
          }
        >
          <TotpPanel />
        </Suspense>
      </section>

      <section className={ui.section} aria-labelledby="security-sessions-title">
        <h2 id="security-sessions-title" className={ui.sectionTitle}>
          {t('security.sessions.title')}
        </h2>
        <Suspense
          fallback={
            <div className={ui.skeletonStack} aria-hidden="true">
              <Skeleton className={styles.skeletonRow} />
              <Skeleton className={styles.skeletonRow} />
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
