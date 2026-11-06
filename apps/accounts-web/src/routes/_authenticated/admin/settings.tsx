/**
 * /admin/settings -- Read-only view of instance settings that are governed
 * by deployment configuration.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { capsOf, useCapabilities } from '../../../features/auth/use-capabilities';
import styles from './settings.module.css';

export function SettingsPage(): ReactElement {
  const { t } = useTranslation('admin');
  const state = useCapabilities();
  const caps = capsOf(state);

  return (
    <div className="aw-stack aw-stack-6" aria-busy={state.status === 'loading' || undefined}>
      <h1 className="aw-page-title">{t('settings.title')}</h1>
      <p className={styles.description}>{t('settings.description')}</p>

      {state.status === 'loading' ? (
        <div className={styles.panel}>
          <VisuallyHidden>{t('common.loading')}</VisuallyHidden>
          <Skeleton className={styles.rowSkeleton} />
        </div>
      ) : null}

      {state.status === 'error' ? (
        <p role="alert" className="aw-error">
          {t('settings.unavailable')}
        </p>
      ) : null}

      {caps ? (
        <dl className={styles.panel}>
          <dt className={styles.term}>{t('settings.registration_open')}</dt>
          <dd className={styles.value}>
            {caps.registrationOpen
              ? t('settings.registration_state_open')
              : t('settings.registration_state_closed')}
          </dd>
          <dd className={styles.help}>{t('settings.registration_open_desc')}</dd>
        </dl>
      ) : null}
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/settings')({
  component: SettingsPage,
});
