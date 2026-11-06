/**
 * One titled group of settings on an account page. `SectionActions` is
 * the trailing status + buttons row of a form.
 */

import type { ReactElement, ReactNode } from 'react';

import styles from './account-page.module.css';

export interface SectionCardProps {
  title: string;
  description?: string;
  children: ReactNode;
}

export default function SectionCard({
  title,
  description,
  children,
}: SectionCardProps): ReactElement {
  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>{title}</h2>
        {description ? <p className={styles.sectionDescription}>{description}</p> : null}
      </div>
      {children}
    </section>
  );
}

export function SectionActions({
  status,
  children,
}: {
  status?: ReactNode;
  children: ReactNode;
}): ReactElement {
  return (
    <div className={styles.actions}>
      <div className={styles.actionsStatus}>{status}</div>
      {children}
    </div>
  );
}
