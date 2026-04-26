/**
 * AIAgentsSkeleton — Suspense fallback for the AI activity section.
 *
 * Renders three placeholder rows that match the column rhythm of the
 * real list, so when the data resolves the layout does not jump.
 */

import type { ReactElement } from 'react';

import styles from './ai-agents.module.css';

export default function AIAgentsSkeleton(): ReactElement {
  return (
    <ul className={styles.skeleton} aria-hidden="true">
      {[0, 1, 2].map((i) => (
        <li key={i} className={styles.skeletonRow}>
          <span className={styles.skeletonChip} style={{ inlineSize: '4rem' }} />
          <span className={styles.skeletonChip} style={{ inlineSize: '60%' }} />
          <span className={styles.skeletonChip} style={{ inlineSize: '3rem' }} />
          <span className={styles.skeletonChip} style={{ inlineSize: '3rem' }} />
          <span className={styles.skeletonChip} style={{ inlineSize: '4rem' }} />
        </li>
      ))}
    </ul>
  );
}
