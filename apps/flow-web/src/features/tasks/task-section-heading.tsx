/**
 * TaskSectionHeading — heading row of a main-column section on the task
 * detail page: the h2 at the start, an optional trailing slot (action,
 * count) and an optional disclosure chevron at the end.
 */

import { ChevronDown } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import css from './task-section-heading.module.css';
import { HintTip } from './task-side-card';

export interface TaskSectionHeadingToggle {
  collapsed: boolean;
  onToggle: () => void;
  /** Id of the region the chevron expands and collapses. */
  controlsId: string;
  /** Already-translated accessible name, e.g. "Expand linked events". */
  label: string;
}

/** Chevron button that expands and collapses a region; shared by section and card headings. */
export function TaskDisclosureToggle({
  collapsed,
  onToggle,
  controlsId,
  label,
}: TaskSectionHeadingToggle): ReactElement {
  return (
    <button
      type="button"
      className={css.toggle}
      aria-expanded={!collapsed}
      aria-controls={controlsId}
      aria-label={label}
      onClick={onToggle}
    >
      <ChevronDown className={css.chevron} size={16} aria-hidden />
    </button>
  );
}

export interface TaskSectionHeadingProps {
  title: string;
  /** Id on the h2, for `aria-labelledby` on the section. */
  id?: string;
  /** Explanation, shown in a tooltip behind an info button next to the title. */
  hint?: string;
  /** Muted count before the trailing slot; sections pass it only when they have no items. */
  count?: number;
  trailing?: ReactNode;
  /** Adds a disclosure chevron after the trailing slot. */
  toggle?: TaskSectionHeadingToggle;
}

export default function TaskSectionHeading({
  title,
  id,
  hint,
  count,
  trailing,
  toggle,
}: TaskSectionHeadingProps): ReactElement {
  return (
    <div className={css.root}>
      <div className={css.titleGroup}>
        <h2 id={id} className={css.title}>
          {title}
        </h2>
        {hint ? <HintTip title={title} hint={hint} /> : null}
      </div>
      {count !== undefined || trailing || toggle ? (
        <div className={css.trailing}>
          {count !== undefined ? <span className={css.count}>{count}</span> : null}
          {trailing}
          {toggle ? <TaskDisclosureToggle {...toggle} /> : null}
        </div>
      ) : null}
    </div>
  );
}
