/**
 * EmptyState — placeholder shown when a list, table, or section has no data.
 *
 * Provides a consistent layout across all apps: optional icon /
 * illustration, title, optional description, and optional action
 * (e.g. a "Create" button or a `<Link>`-wrapped CTA).
 *
 * The primitive is i18n-agnostic — consumers pass already-translated
 * strings or arbitrary `ReactNode` (so an inline-formatted message
 * like `<>{count} items hidden</>` is also valid).
 */

import type { HTMLAttributes, ReactElement, ReactNode } from 'react';
import { cx } from '../../lib/cx';
import styles from './empty-state.module.css';

export interface EmptyStateProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  /**
   * Decorative icon or illustration above the title. Typically an SVG
   * with `aria-hidden="true"` so the title remains the announceable
   * label.
   */
  icon?: ReactNode;
  /** Primary message (e.g. "No tasks yet"). */
  title: ReactNode;
  /**
   * Heading element used for the title. EmptyState always sits inside a
   * page that already owns the document `<h1>`, so the title defaults to
   * `<h2>` for screen-reader navigation. Pass `'p'` to opt out (legacy
   * behaviour) or a different heading level when nesting deeper.
   */
  titleAs?: 'h2' | 'h3' | 'h4' | 'p';
  /** Supporting text below the title. */
  description?: ReactNode;
  /**
   * Call-to-action element (typically a `<Button>`, optionally wrapped
   * in a `<Link>`). Consumers own the rendering so they can compose
   * arbitrary CTAs (single button, multiple buttons, link + button).
   */
  action?: ReactNode;
}

/** EmptyState renders a centered placeholder with icon, title, description, and action. */
export default function EmptyState({
  icon,
  title,
  titleAs = 'h2',
  description,
  action,
  className,
  ...rest
}: EmptyStateProps): ReactElement {
  const TitleTag = titleAs;
  return (
    <div className={cx(styles.root, className)} {...rest}>
      {icon ? <div className={styles.icon}>{icon}</div> : null}
      <TitleTag className={styles.title}>{title}</TitleTag>
      {description ? <p className={styles.description}>{description}</p> : null}
      {action ? <div className={styles.action}>{action}</div> : null}
    </div>
  );
}
