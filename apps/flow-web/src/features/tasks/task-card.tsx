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
      style={{
        padding: '0.875rem',
        display: 'flex',
        flexDirection: 'column',
        gap: '0.5rem',
        cursor: 'grab',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: '0.25rem' }}>
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
          style={{
            flex: 1,
            fontWeight: 600,
            color: 'var(--color-fg)',
            lineHeight: 1.3,
            wordBreak: 'break-word',
            textDecoration: 'none',
          }}
        >
          {task.title}
        </Link>
        <TaskMoveMenu
          state={task.derivedState as TaskDerivedState}
          onTransition={(transition, landingState) =>
            onTransition(task.id, transition, landingState)
          }
        />
      </div>
      <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
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
      </div>
    </Card>
  );
}
