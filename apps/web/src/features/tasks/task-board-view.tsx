/**
 * TaskBoardView — Kanban board grouped by `derivedState`.
 *
 * Native HTML5 drag-and-drop: a task id is set on the dataTransfer payload
 * during dragstart and read during drop. The drop handler invokes
 * `useMoveTask` which currently surfaces a "not yet implemented" toast
 * (see api.ts — backend transitions go through the constraint engine).
 */

import Card from '@nodate-flow/ui/primitives/card';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type DragEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { computeBlockedByOpen, useProjectDependenciesQuery } from '../projects/api';

import {
  TASK_STATES,
  type TaskDerivedState,
  type TaskListItem,
  transitionForDrop,
  useTasksQuery,
  useTransitionTask,
} from './api';
import TaskCard from './task-card';
import { useTaskFilters } from './use-task-filters';

export interface TaskBoardViewProps {
  projectId: string;
}

const STATE_KEY: Record<TaskDerivedState, string> = {
  open: 'tasks.status.open',
  waiting: 'tasks.status.waiting',
  review: 'tasks.status.review',
  done: 'tasks.status.done',
  cancelled: 'tasks.status.cancelled',
};

const DRAG_MIME = 'application/x-nodate-task-id';

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

export default function TaskBoardView({ projectId }: TaskBoardViewProps): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const { data: edges } = useProjectDependenciesQuery(projectId);
  const blockedByOpen = computeBlockedByOpen(edges);
  const transition = useTransitionTask();

  const [draggingId, setDraggingId] = useState<string | null>(null);
  const [hoverState, setHoverState] = useState<TaskDerivedState | null>(null);

  const groups = groupByState(tasks);

  const handleDragStart = (e: DragEvent<HTMLDivElement>, taskId: string): void => {
    e.dataTransfer.setData(DRAG_MIME, taskId);
    e.dataTransfer.effectAllowed = 'move';
    setDraggingId(taskId);
  };

  const handleDragEnd = (): void => {
    setDraggingId(null);
    setHoverState(null);
  };

  const handleDragOver = (e: DragEvent<HTMLElement>, state: TaskDerivedState): void => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (hoverState !== state) setHoverState(state);
  };

  const handleDragLeave = (state: TaskDerivedState): void => {
    if (hoverState === state) setHoverState(null);
  };

  const handleDrop = (e: DragEvent<HTMLElement>, toState: TaskDerivedState): void => {
    e.preventDefault();
    const taskId = e.dataTransfer.getData(DRAG_MIME) || draggingId;
    setDraggingId(null);
    setHoverState(null);
    if (!taskId) return;
    const current = tasks.find((task) => task.id === taskId);
    if (!current) return;
    const fromState = current.derivedState as TaskDerivedState;
    if (fromState === toState) return;
    const resolution = transitionForDrop(fromState, toState);
    if (!resolution) {
      toaster.show({ tone: 'warning', message: t('tasks.errors.illegal_transition') });
      return;
    }
    // When the lenient resolver routes the drop to a column other than the
    // one the user dropped onto (e.g. dropping a `done` card on `open` lands
    // it in `waiting`), tell the user where it actually went so the card
    // jumping columns isn't a surprise.
    if (resolution.landingState !== toState) {
      toaster.show({
        tone: 'info',
        message: t('tasks.board.moved_to', { state: t(STATE_KEY[resolution.landingState]) }),
      });
    }
    transition.mutate(
      {
        id: taskId,
        transition: resolution.transition,
        projectId,
        optimisticState: resolution.landingState,
      },
      {
        onError: () => {
          toaster.show({ tone: 'warning', message: t('tasks.errors.move_failed') });
        },
      },
    );
  };

  const handleSelect = (taskId: string): void => {
    void navigate({ to: '/tasks/$taskId', params: { taskId } });
  };

  return (
    <div
      role="list"
      aria-label={t('tasks.views.board')}
      style={{
        display: 'grid',
        gridAutoFlow: 'column',
        gridAutoColumns: 'minmax(12rem, 1fr)',
        gridTemplateColumns: `repeat(${TASK_STATES.length}, minmax(12rem, 1fr))`,
        gap: '1rem',
        overflowX: 'auto',
        paddingBlockEnd: '1rem',
        maxInlineSize: '100%',
      }}
    >
      {TASK_STATES.map((state) => {
        const items = groups[state];
        const isHover = hoverState === state;
        return (
          <section
            key={state}
            role="listitem"
            aria-label={t(STATE_KEY[state])}
            onDragOver={(e) => {
              handleDragOver(e, state);
            }}
            onDragLeave={() => {
              handleDragLeave(state);
            }}
            onDrop={(e) => {
              handleDrop(e, state);
            }}
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: '0.75rem',
              minBlockSize: '24rem',
            }}
          >
            <Card
              style={{
                padding: '0.75rem 1rem',
                background: isHover ? 'var(--color-surface-raised)' : 'var(--color-surface)',
                borderColor: isHover ? 'var(--color-accent)' : undefined,
              }}
            >
              <header
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: '0.5rem',
                }}
              >
                <span style={{ fontWeight: 600 }}>{t(STATE_KEY[state])}</span>
                <span style={{ color: 'var(--color-muted)', fontVariantNumeric: 'tabular-nums' }}>
                  {items.length}
                </span>
              </header>
            </Card>
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: '0.75rem',
                padding: '0.25rem',
                border: isHover ? '2px dashed var(--color-accent)' : '2px dashed transparent',
                borderRadius: '0.75rem',
                minBlockSize: '6rem',
                transition: 'border-color 120ms ease',
              }}
            >
              {items.length === 0 ? (
                <p
                  style={{
                    margin: 0,
                    padding: '1.25rem 0.75rem',
                    textAlign: 'center',
                    color: 'var(--nf-color-fg-muted, var(--color-muted))',
                    fontSize: '0.8125rem',
                    border: '1px dashed var(--nf-color-border, var(--color-border))',
                    borderRadius: '0.5rem',
                    background: 'var(--nf-color-bg-sunken, transparent)',
                  }}
                >
                  {t('tasks.board.empty_column')}
                </p>
              ) : (
                items.map((task) => (
                  <TaskCard
                    key={task.id}
                    task={task}
                    blockedByOpenCount={blockedByOpen.get(task.id) ?? 0}
                    onDragStart={handleDragStart}
                    onDragEnd={handleDragEnd}
                    onSelect={handleSelect}
                  />
                ))
              )}
            </div>
          </section>
        );
      })}
    </div>
  );
}
