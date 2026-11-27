/**
 * TaskStateBadge — the derived-state badge. It settles each time the state
 * changes after mount, so a transition reads as the result of the click; the
 * first paint is still.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TaskDerivedState } from './api';
import { STATE_KEY, STATE_TONE } from './constants';
import css from './task-state-badge.module.css';

export interface TaskStateBadgeProps {
  state: TaskDerivedState;
}

export default function TaskStateBadge({ state }: TaskStateBadgeProps): ReactElement {
  const { t } = useTranslation('common');
  const [initial] = useState(state);
  return (
    <span key={state} className={cx(css.root, state !== initial && motion.settle)}>
      <Badge tone={STATE_TONE[state]}>{t(STATE_KEY[state])}</Badge>
    </span>
  );
}
