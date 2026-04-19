/**
 * EmptyState — placeholder shown when a list, table, or section has no data.
 *
 * Provides a consistent layout across all apps: optional icon, title,
 * optional description, and optional action (e.g. a "Create" button).
 */

import type { HTMLAttributes, ReactElement, ReactNode } from 'react';
import { cx } from '../../lib/cx';
import styles from './empty-state.module.css';

export interface EmptyStateProps extends HTMLAttributes<HTMLDivElement> {
  /** Decorative icon or illustration above the title. */
  icon?: ReactNode;
  /** Primary message (e.g. "No tasks yet"). */
  title: string;
  /** Supporting text below the title. */
  description?: string;
  /** Call-to-action element (typically a `<Button>`). */
  action?: ReactNode;
}

/** EmptyState renders a centered placeholder with icon, title, description, and action. */
export default function EmptyState({
  icon,
  title,
  description,
  action,
  className,
  ...rest
}: EmptyStateProps): ReactElement {
  return (
    <div className={cx(styles.root, className)} {...rest}>
      {icon ? <div className={styles.icon}>{icon}</div> : null}
      <p className={styles.title}>{title}</p>
      {description ? <p className={styles.description}>{description}</p> : null}
      {action ? <div className={styles.action}>{action}</div> : null}
    </div>
  );
}
