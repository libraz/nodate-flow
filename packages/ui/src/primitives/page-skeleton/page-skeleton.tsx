/**
 * PageSkeleton — full-page loading placeholder.
 *
 * Renders a header bar, optional sidebar column, and a content area
 * filled with Skeleton lines. Use as `<Suspense fallback={<PageSkeleton />}>`.
 */

import type { HTMLAttributes, ReactElement } from 'react';
import { cx } from '../../lib/cx';
import Skeleton from '../skeleton/skeleton';
import styles from './page-skeleton.module.css';

export interface PageSkeletonProps extends HTMLAttributes<HTMLDivElement> {
  /** Show a sidebar skeleton column. */
  sidebar?: boolean;
}

/** PageSkeleton renders a structured loading placeholder for full pages. */
export default function PageSkeleton({
  sidebar = false,
  className,
  ...rest
}: PageSkeletonProps): ReactElement {
  return (
    <div aria-busy="true" className={cx(styles.root, className)} {...rest}>
      {/* Header */}
      <div className={styles.header}>
        <Skeleton style={{ inlineSize: '8rem', blockSize: 'var(--nf-space-5)' }} />
        <Skeleton style={{ inlineSize: '6rem', blockSize: 'var(--nf-space-5)' }} />
      </div>

      <div className={styles.body}>
        {/* Optional sidebar */}
        {sidebar ? (
          <div className={styles.sidebar}>
            <Skeleton style={{ blockSize: 'var(--nf-space-4)' }} />
            <Skeleton style={{ blockSize: 'var(--nf-space-4)', inlineSize: '80%' }} />
            <Skeleton style={{ blockSize: 'var(--nf-space-4)', inlineSize: '60%' }} />
            <Skeleton style={{ blockSize: 'var(--nf-space-4)', inlineSize: '70%' }} />
          </div>
        ) : null}

        {/* Content */}
        <div className={styles.content}>
          <Skeleton style={{ blockSize: 'var(--nf-space-6)', inlineSize: '40%' }} />
          <Skeleton style={{ blockSize: 'var(--nf-space-4)' }} />
          <Skeleton style={{ blockSize: 'var(--nf-space-4)', inlineSize: '90%' }} />
          <Skeleton style={{ blockSize: 'var(--nf-space-4)', inlineSize: '75%' }} />
          <Skeleton style={{ blockSize: 'var(--nf-space-4)', inlineSize: '85%' }} />
        </div>
      </div>
    </div>
  );
}
