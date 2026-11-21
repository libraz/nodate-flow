/**
 * One titled group of settings on an account page. `SectionActions` is
 * the trailing status + buttons row of a form.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { type ReactElement, type ReactNode, useId } from 'react';

import styles from './account-page.module.css';

export interface SectionCardProps {
  /** Omit for a card that is only a framed body (its page header already names it). */
  title?: string;
  description?: string;
  /** `danger` marks a destructive area (delete account, delete workspace). */
  tone?: 'default' | 'danger';
  children: ReactNode;
}

export default function SectionCard({
  title,
  description,
  tone = 'default',
  children,
}: SectionCardProps): ReactElement {
  const titleId = useId();
  const danger = tone === 'danger';
  return (
    <section
      {...(title ? { 'aria-labelledby': titleId } : {})}
      className={cx(styles.section, danger && styles.sectionDanger)}
    >
      {title || description ? (
        <div className={styles.sectionHead}>
          {title ? (
            <h2
              id={titleId}
              className={cx(styles.sectionTitle, danger && styles.sectionTitleDanger)}
            >
              {title}
            </h2>
          ) : null}
          {description ? <p className={styles.sectionDescription}>{description}</p> : null}
        </div>
      ) : null}
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
