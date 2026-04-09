/**
 * TaskCard — compact card used inside the board columns.
 *
 * Shows title, priority badge, and due date pill. The card is draggable
 * (HTML5 native) so the parent board can intercept dragstart/drop events.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import type { DragEvent, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskListItem, TaskPriority } from './api';

export interface TaskCardProps {
  task: TaskListItem;
  /** Count of open `blocks` edges pointing AT this task. 0 hides the badge. */
  blockedByOpenCount?: number;
  onDragStart: (e: DragEvent<HTMLDivElement>, taskId: string) => void;
  onDragEnd: () => void;
  onSelect: (taskId: string) => void;
}

const PRIORITY_TONE: Record<TaskPriority, BadgeTone> = {
  0: 'neutral',
  1: 'info',
  2: 'accent',
  3: 'warning',
  4: 'danger',
};

const PRIORITY_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

export default function TaskCard({
  task,
  blockedByOpenCount = 0,
  onDragStart,
  onDragEnd,
  onSelect,
}: TaskCardProps): ReactElement {
  const { t } = useTranslation('common');
  const priority = (task.priority as TaskPriority) ?? 0;
  const tone = PRIORITY_TONE[priority] ?? 'neutral';
  const priorityLabel = t(PRIORITY_KEY[priority] ?? 'tasks.priority.none');

  return (
    <Card
      draggable
      onDragStart={(e) => {
        onDragStart(e, task.id);
      }}
      onDragEnd={onDragEnd}
      onClick={() => {
        onSelect(task.id);
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onSelect(task.id);
        }
      }}
      role="button"
      tabIndex={0}
      aria-label={task.title}
      style={{
        padding: '0.875rem',
        display: 'flex',
        flexDirection: 'column',
        gap: '0.5rem',
        cursor: 'grab',
      }}
    >
      <div
        style={{
          fontWeight: 600,
          color: 'var(--color-fg)',
          lineHeight: 1.3,
          wordBreak: 'break-word',
        }}
      >
        {task.title}
      </div>
      <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <Badge tone={tone}>{priorityLabel}</Badge>
        {task.dueOn ? (
          <Badge tone="neutral" aria-label={t('tasks.columns.due')}>
            {task.dueOn}
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
