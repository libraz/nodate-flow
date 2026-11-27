/**
 * TaskSectionHeading — heading row of a main-column section on the task
 * detail page: the h2 at the start, an optional trailing slot (action,
 * count) at the end.
 */

import type { ReactElement, ReactNode } from 'react';
import css from './task-section-heading.module.css';

export interface TaskSectionHeadingProps {
  title: string;
  /** Id on the h2, for `aria-labelledby` on the section. */
  id?: string;
  trailing?: ReactNode;
}

export default function TaskSectionHeading({
  title,
  id,
  trailing,
}: TaskSectionHeadingProps): ReactElement {
  return (
    <div className={css.root}>
      <h2 id={id} className={css.title}>
        {title}
      </h2>
      {trailing}
    </div>
  );
}
