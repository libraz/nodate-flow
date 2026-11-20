/**
 * PriorityControl: the one priority picker used by task detail and the
 * create-task dialog. Segments stay neutral until chosen; each carries the
 * priority's colour as a small dot beside its label, from `PRIORITY_COLOR`.
 */

import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { TASK_PRIORITIES, type TaskPriority } from './api';
import { PRIORITY_COLOR, PRIORITY_KEY } from './constants';
import styles from './priority-control.module.css';

/** The segmented control is generic over string values; these bridge the 0–4 ranks. */
type PriorityToken = 'none' | 'low' | 'medium' | 'high' | 'urgent';

const PRIORITY_TO_TOKEN: Record<TaskPriority, PriorityToken> = {
  0: 'none',
  1: 'low',
  2: 'medium',
  3: 'high',
  4: 'urgent',
};

const TOKEN_TO_PRIORITY: Record<PriorityToken, TaskPriority> = {
  none: 0,
  low: 1,
  medium: 2,
  high: 3,
  urgent: 4,
};

export interface PriorityControlProps {
  value: TaskPriority;
  onChange: (next: TaskPriority) => void;
  disabled?: boolean;
}

export default function PriorityControl({
  value,
  onChange,
  disabled = false,
}: PriorityControlProps): ReactElement {
  const { t } = useTranslation('common');
  return (
    <SegmentedControl<PriorityToken>
      ariaLabel={t('tasks.priority.aria_label')}
      size="sm"
      fullWidth
      disabled={disabled}
      value={PRIORITY_TO_TOKEN[value]}
      onChange={(next) => {
        onChange(TOKEN_TO_PRIORITY[next]);
      }}
      options={TASK_PRIORITIES.map((p) => {
        const label = t(PRIORITY_KEY[p]);
        return {
          value: PRIORITY_TO_TOKEN[p],
          ariaLabel: label,
          label: (
            <span className={styles.segment}>
              <span
                aria-hidden="true"
                className={styles.dot}
                style={{ background: PRIORITY_COLOR[p] }}
              />
              <span className={styles.text}>{label}</span>
            </span>
          ),
        };
      })}
    />
  );
}
