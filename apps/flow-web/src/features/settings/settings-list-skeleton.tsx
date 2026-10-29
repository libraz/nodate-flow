/**
 * SettingsListSkeleton — loading placeholder for a settings screen that
 * is a header (title, lede, action) above a list, so the page keeps its
 * shape while the list query suspends.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';

import styles from './settings-list-skeleton.module.css';
import ui from './settings-ui.module.css';

const ROWS = ['a', 'b', 'c'] as const;

export default function SettingsListSkeleton(): ReactElement {
  return (
    <div className={ui.page} aria-hidden="true">
      <div className={ui.header}>
        <div className={ui.headerText}>
          <Skeleton className={styles.title} />
          <Skeleton className={styles.lede} />
        </div>
        <Skeleton className={styles.action} />
      </div>
      <div className={ui.skeletonStack}>
        {ROWS.map((row) => (
          <Skeleton key={row} className={styles.row} />
        ))}
      </div>
    </div>
  );
}
