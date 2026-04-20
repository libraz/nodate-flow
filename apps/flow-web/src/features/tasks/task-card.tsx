/**
 * TaskCard — compact card used inside the board columns.
 *
 * Shows title, priority badge, and due date pill. The card is draggable
 * (HTML5 native) so the parent board can intercept dragstart/drop events.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import { Link } from '@tanstack/react-router';
import { type DragEvent, type ReactElement, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDate, isOverdue } from '../../lib/format';
import type { TaskDerivedState, TaskListItem, TaskPriority, TransitionName } from './api';
import { PRIORITY_KEY, PRIORITY_TONE } from './constants';
import TaskMoveMenu from './task-move-menu';

export interface TaskCardProps {
  task: TaskListItem;
  /** Count of open `blocks` edges pointing AT this task. 0 hides the badge. */
  blockedByOpenCount?: number;
  onDragStart: (e: DragEvent<HTMLDivElement>, taskId: string) => void;
  onDragEnd: () => void;
  onSelect: (taskId: string) => void;
  /** Keyboard-accessible transition handler (from the move menu). */
  onTransition: (
    taskId: string,
    transition: TransitionName,
    landingState: TaskDerivedState,
  ) => void;
}

export default function TaskCard({
  task,
  blockedByOpenCount = 0,
  onDragStart,
  onDragEnd,
  onSelect,
  onTransition,
}: TaskCardProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  // Fields added in Wave 1 — safe access until SDK types are regenerated.
  const ext = task as TaskListItem & {
    projectIdentifier?: string;
    taskNumber?: number;
    labelCount?: number;
  };
  const priority = (task.priority as TaskPriority) ?? 0;
  const tone = PRIORITY_TONE[priority] ?? 'neutral';
  const priorityLabel = t(PRIORITY_KEY[priority] ?? 'tasks.priority.none');

  // HTML5 D&D fires a stray `click` on the drag source after a drag that
  // didn't move far enough, which would otherwise navigate the user into
  // the task detail page mid-drag. We track "did a drag actually start"
  // and suppress the next click on the card body when it did.
  const draggedRef = useRef(false);

  return (
    <Card
      draggable
      onDragStart={(e) => {
        draggedRef.current = true;
        onDragStart(e, task.id);
      }}
      onDragEnd={() => {
        // Reset on the next tick so the synthetic click that fires
        // immediately after dragend (in some browsers) is still
        // suppressed by the click handler below.
        setTimeout(() => {
          draggedRef.current = false;
        }, 0);
        onDragEnd();
      }}
      onClick={(e) => {
        if (draggedRef.current) {
          e.preventDefault();
          return;
        }
        // Title <Link> handles its own navigation and stops propagation;
        // clicks that reach here come from the card's padding / badges.
        onSelect(task.id);
      }}
      className="flex flex-col gap-2 cursor-grab p-3.5"
    >
      <div className="flex items-start gap-1">
        {ext.projectIdentifier && ext.taskNumber ? (
          <span className="shrink-0 rounded bg-[var(--nf-color-bg-muted)] px-1.5 py-0.5 text-xs font-mono text-[var(--nf-color-fg-muted)]">
            {ext.projectIdentifier}-{ext.taskNumber}
          </span>
        ) : null}
        <Link
          to="/tasks/$taskId"
          params={{ taskId: task.id }}
          draggable={false}
          onClick={(e) => {
            // Let modifier-click / middle-click go through natively so
            // the user can open the task in a new tab. Plain click is
            // intercepted so we don't race the board's onSelect handler.
            if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
            e.preventDefault();
            // Stop the click from bubbling to the parent <Card>'s onClick
            // handler, which would otherwise call onSelect a second time.
            e.stopPropagation();
            onSelect(task.id);
          }}
          className="flex-1 font-semibold text-[var(--nf-color-fg)] leading-[1.3] break-words no-underline"
        >
          {task.title}
        </Link>
        <TaskMoveMenu
          state={task.derivedState as TaskDerivedState}
          taskId={task.id}
          onTransition={(transition, landingState) =>
            onTransition(task.id, transition, landingState)
          }
        />
      </div>
      <div className="flex gap-2 items-center flex-wrap">
        {priority > 0 ? <Badge tone={tone}>{priorityLabel}</Badge> : null}
        {task.dueOn ? (
          <Badge
            tone={
              isOverdue(task.dueOn) &&
              task.derivedState !== 'done' &&
              task.derivedState !== 'cancelled'
                ? 'danger'
                : 'neutral'
            }
            aria-label={t('tasks.columns.due')}
          >
            {formatDate(task.dueOn, locale)}
          </Badge>
        ) : null}
        {blockedByOpenCount > 0 ? (
          <Badge
            tone="danger"
            aria-label={t('tasks.card.blockedBy', { count: blockedByOpenCount })}
            title={t('tasks.card.blockedBy', { count: blockedByOpenCount })}
          >
            {`\u{1F512} ${blockedByOpenCount}`}
          </Badge>
        ) : null}
        {ext.labelCount && ext.labelCount > 0 ? (
          <Badge tone="neutral" aria-label={`${ext.labelCount} labels`}>
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 16 16"
              fill="currentColor"
              className="h-3 w-3"
              aria-hidden="true"
            >
              <path
                fillRule="evenodd"
                d="M4.5 2A2.5 2.5 0 0 0 2 4.5v2.879a2.5 2.5 0 0 0 .732 1.767l4.5 4.5a2.5 2.5 0 0 0 3.536 0l2.878-2.878a2.5 2.5 0 0 0 0-3.536l-4.5-4.5A2.5 2.5 0 0 0 7.38 2H4.5ZM5 6a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z"
                clipRule="evenodd"
              />
            </svg>
            {ext.labelCount}
          </Badge>
        ) : null}
      </div>
    </Card>
  );
}
