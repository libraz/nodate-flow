/**
 * ListPageSkeleton — Suspense fallback for the workspace and project list
 * pages. Mirrors their padding, header row and grid so nothing shifts
 * when the list resolves.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';

import styles from './workspaces.module.css';

export interface ListPageSkeletonProps {
  /** True when the list sits inside a layout that already pads the page. */
  nested?: boolean;
}

export default function ListPageSkeleton({ nested = false }: ListPageSkeletonProps): ReactElement {
  return (
    <div className={nested ? styles.section : styles.page} aria-busy="true">
      <div className={styles.pageHeader}>
        <Skeleton className={styles.skeletonTitle} />
        <Skeleton className={styles.skeletonButton} />
      </div>
      <Skeleton className={styles.skeletonGrid} />
    </div>
  );
}
