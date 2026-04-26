/**
 * ArchivedEmptyState — warm "nothing stored yet" placeholder.
 *
 * The illustration is a stroke-only line drawing of an open bookshelf
 * to match the editorial / library framing. The accent line uses the
 * accent token so each theme paints it correctly without per-theme
 * SVG variants.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { Link } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './archived.module.css';

export interface ArchivedEmptyStateProps {
  workspaceId: string;
}

export default function ArchivedEmptyState({ workspaceId }: ArchivedEmptyStateProps): ReactElement {
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
        <rect x="8" y="20" width="80" height="56" rx="3" />
        <path d="M16 36h64M16 56h64" />
        <path d="M22 28v8M30 28v8M40 28v8M48 28v8M58 28v8M68 28v8" />
        <path d="M22 64v8M30 64v8M40 64v8M48 64v8M58 64v8" />
        <path d="M68 64h12" style={{ stroke: 'var(--nf-color-accent)' }} strokeWidth={2} />
      </svg>
      <h2 className={styles.emptyTitle}>{t('empty.noneTitle')}</h2>
      <p className={styles.emptyBody}>{t('empty.noneBody')}</p>
      <Link to="/workspaces/$id/projects" params={{ id: workspaceId }}>
        <Button type="button" variant="primary">
          {t('empty.noneCta')}
        </Button>
      </Link>
    </div>
  );
}
