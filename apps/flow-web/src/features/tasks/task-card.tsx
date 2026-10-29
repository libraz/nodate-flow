/**
 * TaskCard — compact card used inside the board columns.
 *
 * Shows title, priority badge, and due date pill. Drag-and-drop is
 * currently disabled (`draggable=false`); state changes happen through
 * the keyboard-accessible move menu. When D&D is re-enabled in the
 * future, accept `onDragStart` / `onDragEnd` props again and pass
 * `draggable` back to the underlying Card.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import { Link } from '@tanstack/react-router';
import { Lock, Tag } from 'lucide-react';
import type { CSSProperties, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDate, isOverdue } from '../../lib/format';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import type { TaskDerivedState, TaskListItem, TaskPriority, TransitionName } from './api';
import { PRIORITY_KEY, PRIORITY_TONE } from './constants';
import css from './task-card.module.css';
import TaskMoveMenu from './task-move-menu';

export interface TaskCardProps {
  task: TaskListItem;
  /** Count of open `blocks` edges pointing AT this task. 0 hides the badge. */
  blockedByOpenCount?: number;
  onSelect: (taskId: string) => void;
  /** Keyboard-accessible transition handler (from the move menu). */
  onTransition: (
    taskId: string,
    transition: TransitionName,
    landingState: TaskDerivedState,
  ) => void;
  /** Extra class on the card root, e.g. the board's entrance motion. */
  className?: string | undefined;
  style?: CSSProperties | undefined;
}

export default function TaskCard({
  task,
  blockedByOpenCount = 0,
  onSelect,
  onTransition,
  className,
  style,
}: TaskCardProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const zone = useEffectiveZone();
  const locale = i18n.resolvedLanguage ?? 'en';
  // Defensive cast for fields not yet in the regenerated SDK types.
  const ext = task as TaskListItem & {
    projectIdentifier?: string;
    taskNumber?: number;
    labelCount?: number;
  };
  const priority = (task.priority as TaskPriority) ?? 0;
  const tone = PRIORITY_TONE[priority] ?? 'neutral';
  const priorityLabel = t(PRIORITY_KEY[priority] ?? 'tasks.priority.none');
  const closed = task.derivedState === 'done' || task.derivedState === 'cancelled';

  return (
    <Card
      draggable={false}
      onClick={() => {
        // Title <Link> handles its own navigation and stops propagation;
        // clicks that reach here come from the card's padding / badges.
        onSelect(task.id);
      }}
      data-closed={closed}
      className={cx(css.card, className)}
      style={style}
    >
      <div className={css.top}>
        {ext.projectIdentifier && ext.taskNumber ? (
          <span className={css.ref}>
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
          className={css.title}
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
      <div className={css.meta}>
        {priority > 0 ? <Badge tone={tone}>{priorityLabel}</Badge> : null}
        {task.dueOn ? (
          <Badge
            tone={
              isOverdue(task.dueOn, zone) &&
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
            <span className={css.metaIcon}>
              <Lock size={12} aria-hidden />
              {blockedByOpenCount}
            </span>
          </Badge>
        ) : null}
        {ext.labelCount && ext.labelCount > 0 ? (
          <Badge tone="neutral" aria-label={t('labels:task.count', { count: ext.labelCount })}>
            <span className={css.metaIcon}>
              <Tag size={12} aria-hidden />
              {ext.labelCount}
            </span>
          </Badge>
        ) : null}
      </div>
    </Card>
  );
}
