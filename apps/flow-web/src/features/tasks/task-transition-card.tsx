/**
 * TaskTransitionCard — sidebar card that shows the current state and lists
 * the transitions the state machine allows from it. The mutation lives here;
 * the parent only passes which task and state it renders.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ArrowRight, Check, type LucideIcon, Play } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import {
  landingStateFor,
  type TaskDerivedState,
  TRANSITIONS_BY_STATE,
  type TransitionName,
  useTransitionTask,
} from './api';
import { STATE_COLOR, STATE_KEY, TRANSITION_KEY } from './constants';
import { TaskSideCard, TaskSideCardHeading } from './task-side-card';
import TaskStateBadge from './task-state-badge';
import css from './task-transition-card.module.css';

// The forward action worth nudging toward: first of these that is legal from the current state.
const RECOMMENDED_PRIORITY: readonly TransitionName[] = ['complete', 'submit', 'start'];

const RECOMMENDED_ICON: Partial<Record<TransitionName, LucideIcon>> = {
  complete: Check,
  submit: ArrowRight,
  start: Play,
};

export interface TaskTransitionCardProps {
  taskId: string;
  projectId: string;
  state: TaskDerivedState;
}

export default function TaskTransitionCard({
  taskId,
  projectId,
  state,
}: TaskTransitionCardProps): ReactElement {
  const { t } = useTranslation('common');
  const transition = useTransitionTask();
  const pending = transition.isPending;
  // Only the clicked row shows the spinner while every row is locked.
  const pendingName = pending ? transition.variables?.transition : undefined;

  const handleTransition = (name: TransitionName): void => {
    if (pending) return;
    transition.mutate(
      { id: taskId, transition: name, projectId },
      {
        onError: (err) => {
          toaster.show({
            tone: 'warning',
            message: formatApiError(err, t, 'tasks.errors.illegal_transition'),
          });
        },
      },
    );
  };

  const legal = (TRANSITIONS_BY_STATE[state] ?? []).flatMap((name) => {
    const landing = landingStateFor(state, name);
    return landing ? [{ name, landing }] : [];
  });
  const recommended = RECOMMENDED_PRIORITY.find((name) => legal.some((l) => l.name === name));

  return (
    <TaskSideCard>
      <TaskSideCardHeading
        title={t('tasks.detail.transitions.title')}
        hint={t('tasks.detail.transitions.hint')}
      />
      <div className={css.current}>
        <span className={css.currentLabel}>{t('tasks.detail.transitions.current')}</span>
        <TaskStateBadge state={state} />
      </div>
      <div className={css.list} aria-busy={pending ? true : undefined}>
        {legal.map(({ name, landing }) => {
          const RecommendedIcon = name === recommended ? RECOMMENDED_ICON[name] : undefined;
          const clicked = pendingName === name;
          return (
            <Button
              key={name}
              type="button"
              className={cx(
                css.row,
                name === recommended && css.recommended,
                name === 'cancel' && css.cancel,
              )}
              // aria-disabled, not disabled: keyboard focus stays on the row while it works.
              aria-disabled={pending ? true : undefined}
              data-pending={clicked ? '' : undefined}
              onClick={() => {
                handleTransition(name);
              }}
            >
              <span className={css.verb}>
                {RecommendedIcon ? (
                  <RecommendedIcon aria-hidden="true" className={css.icon} />
                ) : null}
                {t(TRANSITION_KEY[name])}
              </span>
              <span className={css.landing}>
                <span aria-hidden="true">→</span>
                {clicked ? (
                  <Spinner
                    size="sm"
                    className={css.spinner}
                    label={t('tasks.detail.transitions.pending')}
                  />
                ) : (
                  <span
                    aria-hidden="true"
                    className={css.dot}
                    style={{ background: STATE_COLOR[landing] }}
                  />
                )}
                {t(STATE_KEY[landing])}
              </span>
            </Button>
          );
        })}
      </div>
    </TaskSideCard>
  );
}
