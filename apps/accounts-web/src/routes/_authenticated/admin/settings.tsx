/**
 * /admin/settings -- Read-only view of instance settings that are governed
 * by deployment configuration.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../../../components/page-header';
import SectionCard from '../../../components/section-card';
import { capsOf, useCapabilities } from '../../../features/auth/use-capabilities';
import styles from './settings.module.css';

export function SettingsPage(): ReactElement {
  const { t } = useTranslation('admin');
  const state = useCapabilities();
  const caps = capsOf(state);

  return (
    <>
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      <div className="aw-stack aw-stack-6" aria-busy={state.status === 'loading' || undefined}>
        {state.status === 'loading' ? (
          <SectionCard title={t('settings.registration_open')}>
            <VisuallyHidden>{t('common.loading')}</VisuallyHidden>
            <Skeleton className={styles.rowSkeleton} />
          </SectionCard>
        ) : null}

        {state.status === 'error' ? (
          <p role="alert" className="aw-error">
            {t('settings.unavailable')}
          </p>
        ) : null}

        {caps ? (
          <SectionCard title={t('settings.registration_open')}>
            <p className={styles.value}>
              {caps.registrationOpen
                ? t('settings.registration_state_open')
                : t('settings.registration_state_closed')}
            </p>
            <p className={styles.help}>{t('settings.registration_open_desc')}</p>
          </SectionCard>
        ) : null}
      </div>
    </>
  );
}

export const Route = createFileRoute('/_authenticated/admin/settings')({
  component: SettingsPage,
});
