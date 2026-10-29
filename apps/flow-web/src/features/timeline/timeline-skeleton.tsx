/**
 * TimelineSkeleton — Suspense fallback for {@link TimelineView}. Mirrors
 * the filter bar, a day heading and a run of event rows so the feed does
 * not jump when it resolves.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';

import styles from './timeline-skeleton.module.css';

const ROWS = ['a', 'b', 'c', 'd', 'e'] as const;

export default function TimelineSkeleton(): ReactElement {
  return (
    <div className={styles.root} aria-busy="true">
      <Skeleton className={styles.filterBar} />
      <Skeleton className={styles.dayHeading} />
      {ROWS.map((row) => (
        <div key={row} className={styles.row}>
          <Skeleton className={styles.avatar} />
          <Skeleton className={styles.card} />
        </div>
      ))}
    </div>
  );
}
