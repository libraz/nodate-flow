/**
 * Page header: the page's level-1 heading, an optional muted
 * description and end-aligned actions that wrap under the title on
 * narrow widths. Pair with `PageFrame`.
 *
 * `help` is a `help` namespace topic: it adds a "?" panel trigger after the
 * title and, when no `description` is given, uses the topic's summary as one.
 */

import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './page-frame.module.css';
import PageHelp from './page-help';

export interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** id on the h1, for an enclosing `aria-labelledby`. */
  titleId?: string;
  /** Topic in the `help` namespace; enables the "?" panel and summary fallback. */
  help?: string;
}

export default function PageHeader({
  title,
  description,
  actions,
  titleId,
  help,
}: PageHeaderProps): ReactElement {
  const { t } = useTranslation('help');
  const summary = help ? t(`${help}.summary`, { defaultValue: '' }) : '';
  const resolvedDescription = description || summary;
  return (
    <header className={styles.header}>
      <div className={styles.heading}>
        <div className={styles.titleRow}>
          <h1 id={titleId} className={styles.title}>
            {title}
          </h1>
          {help ? <PageHelp topic={help} /> : null}
        </div>
        {resolvedDescription ? <p className={styles.description}>{resolvedDescription}</p> : null}
      </div>
      {actions ? <div className={styles.actions}>{actions}</div> : null}
    </header>
  );
}
