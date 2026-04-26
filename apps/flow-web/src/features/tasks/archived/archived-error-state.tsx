/**
 * ArchivedErrorState — fallback shown when the archived-tasks query
 * fails. Centered card-like surface with a single "Retry" CTA that
 * delegates to the parent (which knows whether to refetch the
 * suspense query, reset the error boundary, or both).
 *
 * The illustration mirrors the empty-state SVG language (stroke only,
 * accent line) so the four themes paint the surface coherently
 * without bespoke per-theme art.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './archived.module.css';

export interface ArchivedErrorStateProps {
  onRetry: () => void;
}

export default function ArchivedErrorState({ onRetry }: ArchivedErrorStateProps): ReactElement {
  const { t } = useTranslation('archive');
  return (
    <div className={styles.emptyWrap} role="alert" aria-live="assertive">
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
        <circle cx="48" cy="48" r="32" />
        <path d="M48 32v20" style={{ stroke: 'var(--nf-color-accent)' }} strokeWidth={2} />
        <path d="M48 62v2" style={{ stroke: 'var(--nf-color-accent)' }} strokeWidth={2} />
      </svg>
      <h2 className={styles.emptyTitle}>{t('error.fetchFailed')}</h2>
      <Button type="button" variant="primary" onClick={onRetry}>
        {t('error.retry')}
      </Button>
    </div>
  );
}
