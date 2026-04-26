/**
 * ArchivedSkeleton — loading state shimmer for the Archive page.
 *
 * Three chapter-shaped groups, each containing a chapter-header
 * placeholder and five row-shaped placeholders. Decorative; the
 * `aria-busy` attribute is on the root so AT users hear a single
 * "loading" hint instead of one per row.
 *
 * Uses the shared `Skeleton` primitive, which already ships with the
 * theme-aware shimmer animation and `prefers-reduced-motion` opt-out.
 */

import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement } from 'react';

import styles from './archived.module.css';

const CHAPTER_PLACEHOLDERS = [0, 1, 2] as const;
const ROW_PLACEHOLDERS = [0, 1, 2, 3, 4] as const;

export default function ArchivedSkeleton(): ReactElement {
  return (
    <div aria-busy="true" className={styles.list}>
      {CHAPTER_PLACEHOLDERS.map((chapterId) => (
        <div key={`chapter-${chapterId}`} className={styles.skeletonChapter}>
          <Skeleton className={styles.skeletonHeader} />
          {ROW_PLACEHOLDERS.map((rowId) => (
            <div key={`row-${chapterId}-${rowId}`} className={styles.skeletonRow}>
              <Skeleton style={{ blockSize: '1rem', inlineSize: '1rem' }} />
              <Skeleton style={{ blockSize: '1rem', inlineSize: '70%' }} />
              <Skeleton style={{ blockSize: '1rem', inlineSize: '4rem' }} />
              <Skeleton style={{ blockSize: '1rem', inlineSize: '4rem' }} />
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
