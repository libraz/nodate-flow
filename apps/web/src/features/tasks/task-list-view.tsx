/**
 * TaskListView — DataGrid view of tasks for a project.
 *
 * Columns: title, status, assignee (placeholder until F8 plumbs actors),
 * due date, priority, updated. Rows route to /tasks/$taskId.
 */

import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { useNavigate } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { type TaskDerivedState, type TaskListItem, type TaskPriority, useTasksQuery } from './api';
import { useTaskFilters } from './use-task-filters';

export interface TaskListViewProps {
  projectId: string;
}

const PRIORITY_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

const STATE_KEY: Record<TaskDerivedState, string> = {
  open: 'tasks.status.open',
  waiting: 'tasks.status.waiting',
  review: 'tasks.status.review',
  done: 'tasks.status.done',
  cancelled: 'tasks.status.cancelled',
};

function formatDate(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export default function TaskListView({ projectId }: TaskListViewProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const navigate = useNavigate();
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const locale = i18n.resolvedLanguage ?? 'en';

  const columns: ColumnDef<TaskListItem, unknown>[] = [
    {
      id: 'title',
      accessorKey: 'title',
      header: () => t('tasks.columns.title'),
      cell: ({ row }) => (
        <button
          type="button"
          onClick={() => {
            void navigate({ to: '/tasks/$taskId', params: { taskId: row.original.id } });
          }}
          style={{
            background: 'none',
            border: 'none',
            padding: 0,
            cursor: 'pointer',
            color: 'var(--color-fg)',
            font: 'inherit',
            textAlign: 'start',
          }}
        >
          {row.original.title}
        </button>
      ),
    },
    {
      id: 'status',
      accessorKey: 'derivedState',
      header: () => t('tasks.columns.status'),
      cell: ({ row }) => {
        const state = row.original.derivedState as TaskDerivedState;
        return <span>{t(STATE_KEY[state] ?? 'tasks.status.open')}</span>;
      },
    },
    {
      id: 'assignee',
      header: () => t('tasks.columns.assignee'),
      cell: ({ row }) => {
        const pid = row.original.primaryAssigneeId;
        const count = row.original.assigneeCount;
        if (!pid) return <span style={{ color: 'var(--color-muted)' }}>—</span>;
        const shortId = pid.slice(0, 8);
        const extra = count > 1 ? count - 1 : 0;
        return (
          <span style={{ display: 'inline-flex', gap: '0.5rem', alignItems: 'center' }}>
            <span style={{ fontVariantNumeric: 'tabular-nums' }}>{shortId}</span>
            {extra > 0 ? (
              <span style={{ color: 'var(--color-muted)' }}>
                {t('tasks.assignee.plus_n', { n: extra })}
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      id: 'due',
      accessorKey: 'dueOn',
      header: () => t('tasks.columns.due'),
      cell: ({ row }) => <span>{row.original.dueOn ?? '—'}</span>,
    },
    {
      id: 'priority',
      accessorKey: 'priority',
      header: () => t('tasks.columns.priority'),
      cell: ({ row }) => {
        const p = (row.original.priority as TaskPriority) ?? 0;
        return <span>{t(PRIORITY_KEY[p] ?? 'tasks.priority.none')}</span>;
      },
    },
    {
      id: 'updated',
      accessorKey: 'updatedAt',
      header: () => t('tasks.columns.updated'),
      cell: ({ row }) => (
        <span>{row.original.updatedAt ? formatDate(row.original.updatedAt, locale) : '—'}</span>
      ),
    },
  ];

  return (
    <DataGrid<TaskListItem>
      aria-label={t('tasks.title')}
      columns={columns}
      data={tasks}
      emptyContent={t('tasks.empty')}
      style={{ minBlockSize: '20rem' }}
    />
  );
}
