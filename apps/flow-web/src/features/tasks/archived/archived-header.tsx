/**
 * ArchivedHeader — page title, lede copy, and the "N tasks · last
 * archived X ago" status line.
 *
 * The status line uses the ICU plural inside `archive.page.countWithLast`,
 * so the same key formats `1 task` / `12 tasks` / `12 件のタスク`
 * without per-language code paths. When no archived row carries an
 * `archivedAt` epoch (e.g. backend-side regression) the relative line
 * falls back to a plain count.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './archived.module.css';
import { formatTimeAgo } from './relative-time';

export interface ArchivedHeaderProps {
  count: number;
  /** Latest `archivedAt` epoch across the visible result set, if any. */
  lastArchivedAt: number | undefined;
  /** Caller passes the active i18n locale so we can use Intl.RelativeTimeFormat. */
  locale: string;
}

/**
 * Default export so file-based routing can consume the page tree
 * directly without a re-export shim.
 */
export default function ArchivedHeader({
  count,
  lastArchivedAt,
  locale,
}: ArchivedHeaderProps): ReactElement {
  const { t } = useTranslation('archive');
  const ago = formatTimeAgo(lastArchivedAt, locale);
  return (
    <header className={styles.header}>
      <h1 id="archive-title" className={styles.title}>
        {t('page.title')}
      </h1>
      <p className={styles.subtitle}>{t('page.subtitle')}</p>
      {ago ? (
        <p className={styles.countLine} aria-live="polite">
          {t('page.countWithLast', { count, ago })}
        </p>
      ) : (
        <p className={styles.countLine} aria-live="polite">
          {t('page.countWithLast', { count, ago: '—' })}
        </p>
      )}
    </header>
  );
}
