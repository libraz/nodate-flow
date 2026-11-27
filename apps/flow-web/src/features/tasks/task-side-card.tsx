/**
 * TaskSideCard / TaskSideCardHeading — the sidebar card shell of the task
 * detail page and its heading, shared by every card in the sidebar.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Card, { type CardProps } from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import css from './task-side-card.module.css';

export function TaskSideCard({ className, ...rest }: CardProps): ReactElement {
  return <Card className={cx(css.card, className)} {...rest} />;
}

export interface TaskSideCardHeadingProps {
  title: string;
  hint?: string;
  marker?: ReactElement;
}

/** Card heading with an optional one-paragraph explanation under it. */
export function TaskSideCardHeading({
  title,
  hint,
  marker,
}: TaskSideCardHeadingProps): ReactElement {
  return (
    <div className={css.head}>
      <div className={css.titleRow}>
        <h2 className={css.title}>{title}</h2>
        {marker}
      </div>
      {hint ? <p className={css.hint}>{hint}</p> : null}
    </div>
  );
}
