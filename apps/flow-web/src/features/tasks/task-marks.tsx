/**
 * Hierarchy, count and assignee marks shared by the board card and the list
 * row, so the two views present them identically.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import Badge from '@nodate-flow/ui/primitives/badge';
import { Link } from '@tanstack/react-router';
import { CornerDownRight, ListTree, Lock, MessageSquare } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskListItem } from './api';
import css from './task-marks.module.css';

/** A neutral badge with a 12px icon and a tabular count. */
function CountBadge({
  icon,
  count,
  label,
  tone = 'neutral',
}: {
  icon: ReactNode;
  count: number;
  label: string;
  tone?: 'neutral' | 'danger';
}): ReactElement {
  return (
    <Badge tone={tone} aria-label={label} title={label}>
      <span className={css.countMark}>
        {icon}
        {count}
      </span>
    </Badge>
  );
}

/** One muted line naming the parent of a subtask; nothing for a top-level task. */
export function ParentTaskLine({ task }: { task: TaskListItem }): ReactElement | null {
  const { t } = useTranslation('common');
  if (!task.parentTaskId) return null;
  const label = task.parentTaskTitle || t('tasks.hierarchy.parent_fallback');
  return (
    <Link
      to="/tasks/$taskId"
      params={{ taskId: task.parentTaskId }}
      draggable={false}
      title={label}
      className={css.parentLine}
      onPointerDown={(e) => {
        // A press here opens the parent; it must not arm the card's drag.
        e.stopPropagation();
      }}
      onMouseDown={(e) => {
        e.stopPropagation();
      }}
      onTouchStart={(e) => {
        e.stopPropagation();
      }}
      onClick={(e) => {
        e.stopPropagation();
      }}
    >
      <CornerDownRight size={12} aria-hidden className={css.parentIcon} />
      <span className={css.parentTitle}>{label}</span>
    </Link>
  );
}

/** Direct subtask count; nothing when the task has none. */
export function SubtaskCountBadge({ task }: { task: TaskListItem }): ReactElement | null {
  const { t } = useTranslation('common');
  if (!(task.subtaskCount > 0)) return null;
  return (
    <CountBadge
      icon={<ListTree size={12} aria-hidden />}
      count={task.subtaskCount}
      label={t('tasks.hierarchy.subtasks', { count: task.subtaskCount })}
    />
  );
}

/** Comment count; nothing at zero. */
export function CommentCountBadge({ task }: { task: TaskListItem }): ReactElement | null {
  const { t } = useTranslation('common');
  if (!(task.commentCount > 0)) return null;
  return (
    <CountBadge
      icon={<MessageSquare size={12} aria-hidden />}
      count={task.commentCount}
      label={t('tasks.card.comments', { count: task.commentCount })}
    />
  );
}

/** Open blockers pointing at the task; nothing at zero. */
export function BlockedCountBadge({ count }: { count: number }): ReactElement | null {
  const { t } = useTranslation('common');
  if (!(count > 0)) return null;
  return (
    <CountBadge
      tone="danger"
      icon={<Lock size={12} aria-hidden />}
      count={count}
      label={t('tasks.card.blockedBy', { count })}
    />
  );
}

/** Muted "+N" for what a capped list left out; `srText` and `title` name the rest. */
export function OverflowCount({
  text,
  srText,
  title,
}: {
  text: string;
  srText: string;
  title: string;
}): ReactElement {
  return (
    <span className={css.overflowCount} title={title}>
      <span aria-hidden="true">{text}</span>
      <VisuallyHidden>{srText}</VisuallyHidden>
    </span>
  );
}

/** Most avatars shown before the rest collapse into "+N". */
const MAX_AVATARS = 3;

/** Overlapping avatars for the human assignees; the names are the accessible label. */
export function AssigneeStack({ task }: { task: TaskListItem }): ReactElement | null {
  const { t, i18n } = useTranslation('common');
  const assignees = task.assignees ?? [];
  if (assignees.length === 0) return null;
  const names = new Intl.ListFormat(i18n.resolvedLanguage ?? 'en', { type: 'conjunction' }).format(
    assignees.map((a) => a.displayName),
  );
  const extra = assignees.length - MAX_AVATARS;
  return (
    <span role="img" aria-label={names} title={names} className={css.avatars}>
      {assignees.slice(0, MAX_AVATARS).map((a) => (
        <Avatar
          key={a.id}
          size="sm"
          alt={a.displayName}
          initials={a.displayName.slice(0, 1)}
          aria-hidden="true"
          className={css.avatar}
        />
      ))}
      {extra > 0 ? (
        <span className={css.overflowCount} aria-hidden="true">
          {t('tasks.assignee.plus_n', { n: extra })}
        </span>
      ) : null}
    </span>
  );
}
