/**
 * CompleteTaskButton — circle button that completes an assigned task in place
 * (Today and the home summary). Only open / review tasks can be completed;
 * an in-progress task shows the same button as `aria-disabled` with a reason.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Check } from 'lucide-react';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import {
  landingStateFor,
  type TaskDerivedState,
  type TransitionName,
  useTransitionTask,
} from './api';
import styles from './complete-task-button.module.css';

export interface CompleteTaskButtonProps {
  task: {
    id: string;
    title: string;
    derivedState: TaskDerivedState;
    projectId: string;
  };
}

export default function CompleteTaskButton({ task }: CompleteTaskButtonProps): ReactElement {
  const { t } = useTranslation('common');
  const transition = useTransitionTask();
  const reasonId = useId();
  const completable = landingStateFor(task.derivedState, 'complete') === 'done';
  const pending = transition.isPending;

  const stepArgs = (from: TaskDerivedState, name: TransitionName) => {
    const landing = landingStateFor(from, name);
    return {
      id: task.id,
      transition: name,
      projectId: task.projectId,
      ...(landing ? { optimisticState: landing } : {}),
    };
  };

  // Put the task back where it was: done only reopens into waiting, so an
  // open task needs an unblock and a review task a submit to get home.
  const handleUndo = async (from: TaskDerivedState): Promise<void> => {
    try {
      await transition.mutateAsync(stepArgs('done', 'reopen'));
      await transition.mutateAsync(stepArgs('waiting', from === 'review' ? 'submit' : 'unblock'));
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'today.undo_error') });
    }
  };

  // mutateAsync, not mutate: the row unmounts as soon as the optimistic
  // update lands, and per-call callbacks of an unmounted observer never run.
  const handleComplete = (): void => {
    if (!completable || pending) return;
    const from = task.derivedState;
    transition.mutateAsync(stepArgs(from, 'complete')).then(
      () => {
        toaster.show({
          tone: 'success',
          message: t('today.completed_toast', { title: task.title }),
          action: {
            label: t('common.undo'),
            onAction: () => {
              void handleUndo(from);
            },
          },
        });
      },
      (err: unknown) => {
        toaster.show({ tone: 'danger', message: formatApiError(err, t, 'today.complete_error') });
      },
    );
  };

  return (
    <span className={styles.root}>
      <button
        type="button"
        className={cx(styles.button, 'nf-focus-ring')}
        aria-label={t('today.complete_task', { title: task.title })}
        aria-disabled={!completable || pending ? true : undefined}
        aria-describedby={completable ? undefined : reasonId}
        title={completable ? undefined : t('today.complete_unavailable')}
        data-pending={pending ? '' : undefined}
        onClick={handleComplete}
      >
        <span aria-hidden="true" className={styles.circle}>
          <Check size={12} strokeWidth={2.5} className={styles.check} />
        </span>
      </button>
      {completable ? null : (
        <span id={reasonId} className={styles.reason}>
          {t('today.complete_unavailable')}
        </span>
      )}
    </span>
  );
}
