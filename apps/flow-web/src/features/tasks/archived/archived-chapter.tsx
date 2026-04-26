/**
 * ArchivedChapter — sticky stratum header for one editorial section.
 *
 * Renders an h2 + count badge that sticks to the top of the scroll
 * region as the user reads through the chapter. Visually quiet —
 * uppercase small-caps, hairline divider, tabular numerals — so the
 * chapter heads recede into the page until the reader engages with one.
 *
 * Children are passed through verbatim so the page can plug in either
 * the virtualized row stream or a mocked test array without coupling
 * the chapter chrome to the row component.
 */

import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './archived.module.css';
import type { ChapterId } from './hooks/use-time-strata';

export interface ArchivedChapterProps {
  id: ChapterId;
  count: number;
  children: ReactNode;
}

export default function ArchivedChapter({
  id,
  count,
  children,
}: ArchivedChapterProps): ReactElement {
  const { t } = useTranslation('archive');
  const labelKey = `chapter.${id}` as const;
  return (
    <li className={styles.chapter}>
      <div className={styles.chapterHeader}>
        <h2 className={styles.chapterTitle}>{t(labelKey)}</h2>
        <span className={styles.chapterCount}>{count}</span>
      </div>
      <ul className={styles.chapterRows}>{children}</ul>
    </li>
  );
}
