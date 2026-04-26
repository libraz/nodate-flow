/**
 * ArchivedEmptyFiltered — placeholder when active filters yield zero
 * matches. Distinct copy + CTA from the "never archived anything"
 * empty state so users always know whether the archive itself is empty
 * or whether their filter is too narrow.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './archived.module.css';

export interface ArchivedEmptyFilteredProps {
  onClearFilters: () => void;
}

export default function ArchivedEmptyFiltered({
  onClearFilters,
}: ArchivedEmptyFilteredProps): ReactElement {
  const { t } = useTranslation('archive');
  return (
    <div className={styles.emptyWrap}>
      <svg
        aria-hidden="true"
        viewBox="0 0 96 96"
        className={styles.emptyIllustration}
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <circle cx="42" cy="42" r="22" />
        <path d="M58 58l18 18" />
        <path d="M30 42h24" style={{ stroke: 'var(--nf-color-accent)' }} strokeWidth={2} />
      </svg>
      <h2 className={styles.emptyTitle}>{t('empty.filteredTitle')}</h2>
      <p className={styles.emptyBody}>{t('empty.filteredBody')}</p>
      <Button type="button" variant="primary" onClick={onClearFilters}>
        {t('empty.filteredCta')}
      </Button>
    </div>
  );
}
