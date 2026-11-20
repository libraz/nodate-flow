/**
 * Compact assignee cell: monogram + first display name, "+N more" when there
 * are more, full list in a tooltip. Falls back to a head count when only
 * non-human assignees exist.
 */
import Avatar from '@nodate-flow/ui/primitives/avatar';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import type { TaskListItem } from './api';

export default function AssigneeSummary({
  task,
  emptyClassName,
}: {
  task: TaskListItem;
  emptyClassName?: string | undefined;
}): ReactElement {
  const { t } = useTranslation('common');
  const { assigneeCount } = task;
  const assignees = task.assignees ?? [];
  const first = assignees[0];
  if (first === undefined) {
    if (assigneeCount === 0) return <span className={emptyClassName}>—</span>;
    return <span>{t('tasks.assignee.count', { count: assigneeCount })}</span>;
  }
  const more = assignees.length - 1;
  return (
    <Tooltip content={assignees.map((a) => a.displayName).join(', ')}>
      <span
        style={{
          display: 'inline-flex',
          gap: 'var(--nf-space-1-5)',
          alignItems: 'center',
          minInlineSize: 0,
          maxInlineSize: '100%',
        }}
      >
        <Avatar size="sm" alt={first.displayName} initials={first.displayName.slice(0, 1)} />
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {first.displayName}
        </span>
        {more > 0 && (
          <span style={{ flexShrink: 0, color: 'var(--nf-color-fg-muted)' }}>
            {t('tasks.assignee.plus_n', { n: more })}
          </span>
        )}
      </span>
    </Tooltip>
  );
}
