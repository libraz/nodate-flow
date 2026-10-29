/**
 * Branded "forbidden" component rendered when the authenticated user
 * lacks permission to view a protected resource (HTTP 403). Mirrors
 * the NotFound component's visual treatment so deep-link 403s land on
 * a polished state instead of the generic ErrorBoundary.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './status-page.module.css';

export default function Forbidden(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section className={styles.root}>
      <div aria-hidden className={styles.code}>
        {t('forbidden.code', { defaultValue: '403' })}
      </div>
      <h1 className={styles.title}>{t('forbidden.title')}</h1>
      <p className={styles.description}>{t('forbidden.description')}</p>
      <Link to="/" className={cx(styles.home, 'nf-focus-ring')}>
        {t('forbidden.back_home')}
      </Link>
    </section>
  );
}
