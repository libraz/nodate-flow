/**
 * Branded "not found" component used by both the root route's
 * `notFoundComponent` and the authenticated layout's
 * `notFoundComponent`. Renders a large gradient 404 mark, a short
 * description, and a back-home button.
 *
 * The authenticated layout wraps this in `<AppShell>` so deep-link 404s
 * land inside the normal sidebar/topbar chrome instead of replacing the
 * entire viewport with a bare error message.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './status-page.module.css';

export default function NotFound(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section className={styles.root}>
      <div aria-hidden className={styles.code}>
        {t('not_found.code', { defaultValue: '404' })}
      </div>
      <h1 className={styles.title}>{t('not_found.title')}</h1>
      <p className={styles.description}>{t('not_found.description')}</p>
      <Link to="/" className={cx(styles.home, 'nf-focus-ring')}>
        {t('not_found.back_home')}
      </Link>
    </section>
  );
}
