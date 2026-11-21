/**
 * StateDot — the colour dot beside a derived-state label.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import type { ReactElement } from 'react';
import type { TaskDerivedState } from './api';
import { STATE_COLOR } from './constants';
import styles from './task-marks.module.css';

export interface StateDotProps {
  state: TaskDerivedState;
  className?: string | undefined;
}

export default function StateDot({ state, className }: StateDotProps): ReactElement {
  return (
    <span
      aria-hidden="true"
      className={cx(styles.stateDot, className)}
      style={{ background: STATE_COLOR[state] ?? STATE_COLOR.open }}
    />
  );
}
