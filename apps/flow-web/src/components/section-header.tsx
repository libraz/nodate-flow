/**
 * Section header: the h2-level counterpart of `PageHeader` for a tab or
 * section inside a page that already owns the h1. Same anatomy: title with
 * an optional "?" help trigger, a muted description and end-aligned actions.
 */

import type { ReactElement, ReactNode } from 'react';
import PageHelp from './page-help';
import styles from './section-header.module.css';

export interface SectionHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** Topic in the `help` namespace; enables the "?" panel. */
  help?: string;
}

export default function SectionHeader({
  title,
  description,
  actions,
  help,
}: SectionHeaderProps): ReactElement {
  return (
    <header className={styles.header}>
      <div className={styles.heading}>
        <div className={styles.titleRow}>
          <h2 className={styles.title}>{title}</h2>
          {help ? <PageHelp topic={help} /> : null}
        </div>
        {description ? <p className={styles.description}>{description}</p> : null}
      </div>
      {actions ? <div className={styles.actions}>{actions}</div> : null}
    </header>
  );
}
