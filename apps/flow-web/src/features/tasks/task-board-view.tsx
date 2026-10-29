/**
 * TaskBoardView — Kanban board grouped by `derivedState`.
 *
 * Drag-and-drop is currently disabled: until the constraint engine fully
 * supports D&D state transitions, cards are non-draggable (`draggable=false`)
 * and the per-column drop targets are inert. State changes go through the
 * keyboard / pointer-accessible move menu on each card, which posts to
 * `POST /tasks/{id}/transitions`. A short hint near each column header
 * advertises this. Re-enabling is a one-line revert: drop the
 * `dndDisabled` flag and pass the drag handlers back to TaskCard.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Card from '@nodate-flow/ui/primitives/card';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useNavigate } from '@tanstack/react-router';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { computeBlockedByOpen, useProjectDependenciesQuery } from '../projects/api';
import {
  TASK_STATES,
  TASKS_QUERY_LIMIT,
  type TaskDerivedState,
  type TaskListItem,
  type TransitionName,
  useTasksQuery,
  useTransitionTask,
} from './api';
import { STATE_KEY } from './constants';
import css from './task-board-view.module.css';
import TaskCard, { type TaskCardProps } from './task-card';
import { useTaskFilters } from './use-task-filters';

export interface TaskBoardViewProps {
  projectId: string;
}

function groupByState(tasks: readonly TaskListItem[]): Record<TaskDerivedState, TaskListItem[]> {
  const groups: Record<TaskDerivedState, TaskListItem[]> = {
    open: [],
    waiting: [],
    review: [],
    done: [],
    cancelled: [],
  };
  for (const task of tasks) {
    const state = task.derivedState as TaskDerivedState;
    const bucket = groups[state];
    if (bucket) bucket.push(task);
  }
  return groups;
}

/**
 * The entrance is picked once, at mount: the first-load stagger ends
 * after a few hundred ms and the class must not flip under a card that
 * has already arrived, or the new animation would replay.
 */
function BoardCard({
  arrivingDone,
  ...props
}: TaskCardProps & { arrivingDone: boolean }): ReactElement {
  const [entrance] = useState(() => (arrivingDone ? motion.settle : motion.enter));
  return <TaskCard {...props} className={entrance} />;
}

interface BoardColumnListProps {
  state: TaskDerivedState;
  items: TaskListItem[];
  blockedByOpen: Map<string, number>;
  staggerFor: (index: number) => React.CSSProperties | undefined;
  onSelect: (taskId: string) => void;
  onTransition: (
    taskId: string,
    transition: TransitionName,
    landingState: TaskDerivedState,
  ) => void;
}

/**
 * One column's cards. A card that leaves (moved to another state,
 * archived) collapses out while its copy enters the destination column,
 * so a move reads as a move; a card that lands in Done settles instead
 * of sliding, which is the completion beat.
 */
function BoardColumnList({
  state,
  items,
  blockedByOpen,
  staggerFor,
  onSelect,
  onTransition,
}: BoardColumnListProps): ReactElement {
  const { t } = useTranslation('common');
  const { entries, onExited } = usePresenceList(items, (task) => task.id);

  return (
    <div role="list" aria-label={t(STATE_KEY[state])} className={css.dropZone}>
      {entries.length === 0 ? (
        <div role="listitem" className={css.emptyColumn}>
          {t('tasks.board.empty_column')}
        </div>
      ) : (
        entries.map(({ key, item: task, present }, index) => {
          const stagger = staggerFor(index);
          return (
            <PresenceItem
              key={key}
              role="listitem"
              present={present}
              onExited={() => onExited(key)}
            >
              <BoardCard
                task={task}
                blockedByOpenCount={blockedByOpen.get(task.id) ?? 0}
                onSelect={onSelect}
                onTransition={onTransition}
                arrivingDone={!stagger && state === 'done'}
                {...(stagger ? { style: stagger } : {})}
              />
            </PresenceItem>
          );
        })
      )}
    </div>
  );
}

export default function TaskBoardView({ projectId }: TaskBoardViewProps): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const { data: edges } = useProjectDependenciesQuery(projectId);
  const blockedByOpen = computeBlockedByOpen(edges);
  const transition = useTransitionTask();
  const staggerFor = useFirstLoadStagger();

  const groups = groupByState(tasks);
  const mayBeTruncated = tasks.length >= TASKS_QUERY_LIMIT;

  const handleTransition = useCallback(
    (taskId: string, transitionName: TransitionName, landingState: TaskDerivedState): void => {
      transition.mutate(
        {
          id: taskId,
          transition: transitionName,
          projectId,
          optimisticState: landingState,
        },
        {
          onError: (err) => {
            toaster.show({
              tone: 'warning',
              message: formatApiError(err, t, 'tasks.errors.move_failed'),
            });
          },
        },
      );
    },
    [transition, projectId, t],
  );

  const handleSelect = (taskId: string): void => {
    void navigate({ to: '/tasks/$taskId', params: { taskId } });
  };

  return (
    <>
      {mayBeTruncated ? (
        <p className={css.limitNotice}>{t('tasks.limit_notice', { count: TASKS_QUERY_LIMIT })}</p>
      ) : null}
      <div
        role="region"
        aria-label={t('tasks.views.board')}
        className={css.board}
        style={{ '--col-count': TASK_STATES.length } as React.CSSProperties}
      >
        {TASK_STATES.map((state, index) => {
          const items = groups[state];
          return (
            <section key={state} aria-label={t(STATE_KEY[state])} className={css.column}>
              <Card className={css.headerCard}>
                <header className={css.columnHeader}>
                  <span className={css.columnHeaderLabel}>{t(STATE_KEY[state])}</span>
                  <span className={css.columnHeaderCount}>{items.length}</span>
                </header>
                {/* Show the D&D-disabled hint once, on the first column,
                    so it isn't repeated five times across the board. */}
                {index === 0 ? (
                  <p className={css.dndHint}>{t('tasks.board.dnd_disabled_hint')}</p>
                ) : null}
              </Card>
              <BoardColumnList
                state={state}
                items={items}
                blockedByOpen={blockedByOpen}
                staggerFor={staggerFor}
                onSelect={handleSelect}
                onTransition={handleTransition}
              />
            </section>
          );
        })}
      </div>
    </>
  );
}
