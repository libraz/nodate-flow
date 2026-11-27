/**
 * Heading for a project tab (Tasks, Gantt, Timeline). The project name above
 * is the page's h1 and the tab row already says which tab is open, so the h2
 * is for assistive technology only; the tab's help lives behind the project
 * header's "?". Children form the tab's single toolbar row.
 */

import type { ReactElement, ReactNode } from 'react';

import styles from './project-section-header.module.css';

export interface ProjectSectionHeaderProps {
  title: string;
  /** The tab's toolbar controls; omit when the tab has none. */
  children?: ReactNode;
}

export default function ProjectSectionHeader({
  title,
  children,
}: ProjectSectionHeaderProps): ReactElement {
  return (
    <>
      <h2 className={styles.title}>{title}</h2>
      {children ? <div className={styles.toolbar}>{children}</div> : null}
    </>
  );
}
