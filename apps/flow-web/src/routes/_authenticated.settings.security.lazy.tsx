/**
 * /settings/security — user security settings (lazy): password change,
 * TOTP 2FA, and the active sessions list, one bordered section each.
 */

import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createLazyFileRoute } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../components/page-header';
import { selectUser, useAuth } from '../features/auth/auth-store';
import { useAuthCapabilitiesQuery } from '../features/settings/api';
import PasswordPanel from '../features/settings/password-panel';
import SessionsPanel from '../features/settings/sessions-panel';
import ui from '../features/settings/settings-ui.module.css';
import TotpPanel from '../features/settings/totp-panel';
import styles from './_authenticated.settings.security.module.css';

function SecurityRoute(): ReactElement {
  const { t } = useTranslation('settings');
  // Password change and two-factor management depend on the capabilities
  // and on the account holding a local password; they render only once
  // both are known.
  const capabilities = useAuthCapabilitiesQuery();
  const hasPassword = useAuth(selectUser)?.hasPassword === true;
  const passwordLogin = capabilities.data?.passwordLogin === true && hasPassword;
  const totpManagement = capabilities.data?.totp === true && hasPassword;
  return (
    <div className={ui.page}>
      <PageHeader title={t('security.title')} />

      {capabilities.isError && (
        <ErrorFallback
          title={t('security.load_error')}
          action={{
            label: t('security.retry'),
            onClick: () => {
              void capabilities.refetch();
            },
          }}
          error={capabilities.error}
        />
      )}

      {passwordLogin && (
        <section className={ui.section} aria-labelledby="security-password-title">
          <h2 id="security-password-title" className={ui.sectionTitle}>
            {t('security.password.title')}
          </h2>
          <PasswordPanel />
        </section>
      )}

      {totpManagement && (
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
      )}

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
