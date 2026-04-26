/**
 * LinkedEventsSkeleton — shimmer placeholder rendered while
 * `useLinkedEventsQuery` suspends.
 *
 * Three rows of three chips each (glyph / title / time) approximating
 * the final list density. Decorative — the parent Suspense boundary
 * already advertises the loading state to assistive technology.
 */

import type { CSSProperties, ReactElement } from 'react';

import styles from './linked-events.module.css';

const ROW_TITLE_WIDTHS: readonly string[] = ['62%', '48%', '70%'];

export default function LinkedEventsSkeleton(): ReactElement {
  return (
    <div className={styles.skeleton} aria-hidden="true">
      {ROW_TITLE_WIDTHS.map((titleWidth, idx) => {
        const titleStyle: CSSProperties = { inlineSize: titleWidth };
        const glyphStyle: CSSProperties = { inlineSize: '0.875rem' };
        const timeStyle: CSSProperties = { inlineSize: '4rem' };
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: static-length skeleton, no reorder
          <div key={idx} className={styles.skeletonRow}>
            <div className={styles.skeletonChip} style={glyphStyle} />
            <div className={styles.skeletonChip} style={titleStyle} />
            <div className={styles.skeletonChip} style={timeStyle} />
          </div>
        );
      })}
    </div>
  );
}
