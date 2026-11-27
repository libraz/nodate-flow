/**
 * Page header: the page's level-1 heading, an optional one-line muted
 * description and end-aligned actions that wrap under the title on
 * narrow widths. Pair with `PageFrame`.
 *
 * `help` is a `help` namespace topic: it adds a "?" panel trigger after the
 * title. The topic's summary lives only inside that panel, never under the
 * title. `description` is for page data (e.g. a project's own description)
 * and is clamped to one line; a string description keeps its full text in
 * the `title` tooltip.
 */

import type { ReactElement, ReactNode } from 'react';
import styles from './page-frame.module.css';
import PageHelp from './page-help';

export interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** id on the h1, for an enclosing `aria-labelledby`. */
  titleId?: string;
  /** Topic in the `help` namespace; enables the "?" panel. */
  help?: string;
}

export default function PageHeader({
  title,
  description,
  actions,
  titleId,
  help,
}: PageHeaderProps): ReactElement {
  return (
    <header className={styles.header}>
      <div className={styles.heading}>
        <div className={styles.titleRow}>
          <h1 id={titleId} className={styles.title}>
            {title}
          </h1>
          {help ? <PageHelp topic={help} /> : null}
        </div>
        {description ? (
          <p
            className={styles.description}
            title={typeof description === 'string' ? description : undefined}
          >
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className={styles.actions}>{actions}</div> : null}
    </header>
  );
}
