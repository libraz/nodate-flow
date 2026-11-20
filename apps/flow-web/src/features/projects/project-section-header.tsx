/**
 * Heading row for a project tab (Tasks, Gantt, Timeline): an h2 with its
 * help trigger and description on the start side, the tab's controls on the
 * end side. The project name above is the page's h1.
 */

import type { ReactElement, ReactNode } from 'react';

import PageHelp from '../../components/page-help';
import styles from './project-section-header.module.css';

export interface ProjectSectionHeaderProps {
  title: string;
  /** Topic in the `help` namespace. */
  help: string;
  description: string;
  actions?: ReactNode;
}

export default function ProjectSectionHeader({
  title,
  help,
  description,
  actions,
}: ProjectSectionHeaderProps): ReactElement {
  return (
    <header className={styles.header}>
      <div className={styles.heading}>
        <div className={styles.titleRow}>
          <h2 className={styles.title}>{title}</h2>
          <PageHelp topic={help} />
        </div>
        <p className={styles.description}>{description}</p>
      </div>
      {actions ? <div className={styles.actions}>{actions}</div> : null}
    </header>
  );
}
