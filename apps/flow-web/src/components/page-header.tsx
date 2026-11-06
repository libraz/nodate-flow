/**
 * Page header: the page's level-1 heading, an optional muted
 * description and end-aligned actions that wrap under the title on
 * narrow widths. Pair with `PageFrame`.
 */

import type { ReactElement, ReactNode } from 'react';

import styles from './page-frame.module.css';

export interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** id on the h1, for an enclosing `aria-labelledby`. */
  titleId?: string;
}

export default function PageHeader({
  title,
  description,
  actions,
  titleId,
}: PageHeaderProps): ReactElement {
  return (
    <header className={styles.header}>
      <div className={styles.heading}>
        <h1 id={titleId} className={styles.title}>
          {title}
        </h1>
        {description ? <p className={styles.description}>{description}</p> : null}
      </div>
      {actions ? <div className={styles.actions}>{actions}</div> : null}
    </header>
  );
}
