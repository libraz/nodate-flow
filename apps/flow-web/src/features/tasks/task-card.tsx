/**
 * TaskCard — compact card used inside the board columns.
 *
 * Shows title, priority, due date, count badges, label chips and assignees. On
 * the board it is the drag source: `dragHandle` wires pointer, touch and
 * keyboard dragging. The title link is the card's keyboard handle (Space lifts,
 * Enter opens), so a card costs two tab stops: the link and the move menu.
 * Native HTML5 drag stays off.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import { Link } from '@tanstack/react-router';
import type { CSSProperties, DOMAttributes, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDate, isOverdue } from '../../lib/format';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import type { Label } from '../labels/api';
import LabelBadge from '../labels/label-badge';
import type { TaskDerivedState, TaskListItem, TaskPriority, TransitionName } from './api';
import { PRIORITY_KEY, PRIORITY_TONE } from './constants';
import css from './task-card.module.css';
import {
  AssigneeStack,
  BlockedCountBadge,
  CommentCountBadge,
  OverflowCount,
  ParentTaskLine,
  SubtaskCountBadge,
} from './task-marks';
import TaskMoveMenu from './task-move-menu';

/** The dnd-kit draggable wiring handed to the card. */
export interface TaskCardDragHandle {
  setNodeRef: (node: HTMLElement | null) => void;
  /** The title link: the element the keyboard sensor listens on. */
  setActivatorNodeRef: (node: HTMLElement | null) => void;
  describedBy: string | undefined;
  roleDescription: string | undefined;
  listeners: DOMAttributes<HTMLDivElement> | undefined;
}

/** Most label chips shown before the rest collapse into "+N". */
const MAX_LABEL_CHIPS = 3;

export interface TaskCardProps {
  task: TaskListItem;
  /** Workspace labels already resolved for this task. */
  labels?: readonly Label[];
  /** Makes the card a dnd-kit drag source. */
  dragHandle?: TaskCardDragHandle;
  /** This card is the source of the drag in flight: shown as a placeholder. */
  dragging?: boolean;
  /** A touch is held on the card, waiting out the lift delay. */
  holding?: boolean;
  /** Rendered inside the DragOverlay: lifted, inert. */
  overlay?: boolean;
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
  labels = [],
  dragHandle,
  dragging = false,
  holding = false,
  overlay = false,
  blockedByOpenCount = 0,
  onSelect,
  onTransition,
  className,
  style,
}: TaskCardProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const zone = useEffectiveZone();
  const locale = i18n.resolvedLanguage ?? 'en';
  const priority = (task.priority as TaskPriority) ?? 0;
  const tone = PRIORITY_TONE[priority] ?? 'neutral';
  const priorityLabel = t(PRIORITY_KEY[priority] ?? 'tasks.priority.none');
  const hiddenLabels = labels.slice(MAX_LABEL_CHIPS);
  const listFormat = new Intl.ListFormat(locale, { type: 'conjunction' });
  const hiddenNames = listFormat.format(hiddenLabels.map((label) => label.name));
  const closed = task.derivedState === 'done' || task.derivedState === 'cancelled';

  return (
    <Card
      {...dragHandle?.listeners}
      {...(dragHandle
        ? { ref: dragHandle.setNodeRef, role: 'group', 'aria-label': task.title }
        : {})}
      draggable={false}
      inert={overlay || undefined}
      data-task-id={task.id}
      data-dragging={dragging || undefined}
      data-holding={holding || undefined}
      data-overlay={overlay || undefined}
      onClick={() => {
        // Title <Link> handles its own navigation and stops propagation;
        // clicks that reach here come from the card's padding / badges.
        onSelect(task.id);
      }}
      data-closed={closed}
      className={cx(css.card, className)}
      style={style}
    >
      <ParentTaskLine task={task} />
      <div className={css.top}>
        {task.projectIdentifier && task.taskNumber ? (
          <span className={css.ref}>
            {task.projectIdentifier}-{task.taskNumber}
          </span>
        ) : null}
        <Link
          to="/tasks/$taskId"
          params={{ taskId: task.id }}
          draggable={false}
          data-task-handle={task.id}
          {...(dragHandle
            ? {
                ref: dragHandle.setActivatorNodeRef,
                'aria-describedby': dragHandle.describedBy,
                'aria-roledescription': dragHandle.roleDescription,
              }
            : {})}
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
            tone={isOverdue(task.dueOn, zone) && !closed ? 'danger' : 'neutral'}
            aria-label={`${t('tasks.columns.due')}: ${formatDate(task.dueOn, locale)}`}
          >
            {formatDate(task.dueOn, locale)}
          </Badge>
        ) : null}
        <BlockedCountBadge count={blockedByOpenCount} />
        <SubtaskCountBadge task={task} />
        <CommentCountBadge task={task} />
        {labels.slice(0, MAX_LABEL_CHIPS).map((label) => (
          <span key={label.id} className={css.labelChip} title={label.name}>
            <LabelBadge size="sm" name={label.name} color={label.color} />
          </span>
        ))}
        {hiddenLabels.length > 0 ? (
          <OverflowCount
            text={t('tasks.card.labels_more', { count: hiddenLabels.length })}
            srText={t('tasks.card.labels_more_aria', {
              count: hiddenLabels.length,
              names: hiddenNames,
            })}
            title={hiddenNames}
          />
        ) : null}
        <AssigneeStack task={task} />
      </div>
    </Card>
  );
}
