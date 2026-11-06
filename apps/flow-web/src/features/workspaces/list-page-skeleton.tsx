/**
 * ListPageSkeleton — Suspense fallback for the workspace and project list
 * pages. Mirrors their padding, header row and grid so nothing shifts
 * when the list resolves.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';

import PageFrame from '../../components/page-frame';
import styles from './workspaces.module.css';

export interface ListPageSkeletonProps {
  /** True when the list sits inside a layout that already provides the page frame. */
  nested?: boolean;
}

export default function ListPageSkeleton({ nested = false }: ListPageSkeletonProps): ReactElement {
  const header = (
    <div className={styles.pageHeader}>
      <Skeleton className={styles.skeletonTitle} />
      <Skeleton className={styles.skeletonButton} />
    </div>
  );
  const grid = <Skeleton className={styles.skeletonGrid} />;
  if (nested) {
    return (
      <div className={styles.section} aria-busy="true">
        {header}
        {grid}
      </div>
    );
  }
  return (
    <PageFrame aria-busy="true">
      {header}
      {grid}
    </PageFrame>
  );
}
