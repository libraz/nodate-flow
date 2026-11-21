/**
 * PriorityMark — the colour bar beside a priority label.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import type { ReactElement } from 'react';
import type { TaskPriority } from './api';
import { PRIORITY_COLOR } from './constants';
import styles from './task-marks.module.css';

export interface PriorityMarkProps {
  priority: TaskPriority;
  className?: string | undefined;
}

export default function PriorityMark({ priority, className }: PriorityMarkProps): ReactElement {
  return (
    <span
      aria-hidden="true"
      className={cx(styles.priorityMark, className)}
      style={{ background: PRIORITY_COLOR[priority] ?? PRIORITY_COLOR[0] }}
    />
  );
}
