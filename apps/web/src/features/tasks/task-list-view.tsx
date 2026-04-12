/**
 * TaskListView — DataGrid view of tasks for a project.
 *
 * Columns: title, status, deps, assignee, due date, priority, updated.
 * Features: column sorting (built into DataGrid), priority color indicators,
 * status badges, overdue date highlighting.
 */

import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { Link } from '@tanstack/react-router';
import type { ColumnDef } from '@tanstack/react-table';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { computeBlockedByOpen, useProjectDependenciesQuery } from '../projects/api';

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

const PRIORITY_COLOR: Record<TaskPriority, string> = {
  0: 'var(--color-muted)',
  1: '#3498db',
  2: '#e67e22',
  3: '#e74c3c',
  4: '#c0392b',
};

const STATE_KEY: Record<TaskDerivedState, string> = {
  open: 'tasks.status.open',
  waiting: 'tasks.status.waiting',
  review: 'tasks.status.review',
  done: 'tasks.status.done',
  cancelled: 'tasks.status.cancelled',
};

const STATE_COLOR: Record<TaskDerivedState, string> = {
  open: '#3498db',
  waiting: '#e67e22',
  review: '#9b59b6',
  done: '#27ae60',
  cancelled: '#95a5a6',
};

function formatDate(iso: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(iso));
  } catch {
    return iso;
  }
}

function isOverdue(dueOn: string | undefined | null): boolean {
  if (!dueOn) return false;
  const now = new Date();
  const todayKey = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  return dueOn < todayKey;
}

export default function TaskListView({ projectId }: TaskListViewProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const { data: edges } = useProjectDependenciesQuery(projectId);
  const blockedByOpen = computeBlockedByOpen(edges);
  const locale = i18n.resolvedLanguage ?? 'en';

  const columns: ColumnDef<TaskListItem, unknown>[] = [
    {
      id: 'title',
      accessorKey: 'title',
      size: 320,
      header: () => t('tasks.columns.title'),
      cell: ({ row }) => (
        <Link
          to="/tasks/$taskId"
          params={{ taskId: row.original.id }}
          style={{
            color: 'var(--color-fg)',
            textDecoration: 'none',
            fontWeight: 500,
          }}
        >
          {row.original.title}
        </Link>
      ),
    },
    {
      id: 'status',
      accessorKey: 'derivedState',
      size: 100,
      header: () => t('tasks.columns.status'),
      cell: ({ row }) => {
        const state = row.original.derivedState as TaskDerivedState;
        const color = STATE_COLOR[state] ?? STATE_COLOR.open;
        return (
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.375rem',
              fontSize: '0.8125rem',
            }}
          >
            <span
              aria-hidden
              style={{
                width: '0.5rem',
                height: '0.5rem',
                borderRadius: '999px',
                background: color,
                flexShrink: 0,
              }}
            />
            {t(STATE_KEY[state] ?? 'tasks.status.open')}
          </span>
        );
      },
    },
    {
      id: 'deps',
      size: 60,
      enableSorting: false,
      header: () => t('tasks.columns.deps'),
      cell: ({ row }) => {
        const count = blockedByOpen.get(row.original.id) ?? 0;
        if (count === 0) return <span style={{ color: 'var(--color-muted)' }}>—</span>;
        return (
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.25rem',
              color: 'var(--nf-color-danger, #c0392b)',
              fontVariantNumeric: 'tabular-nums',
            }}
            title={t('tasks.card.blockedBy', { count })}
          >
            {`\u{1F512} ${count}`}
          </span>
        );
      },
    },
    {
      id: 'assignee',
      size: 110,
      enableSorting: false,
      header: () => t('tasks.columns.assignee'),
      cell: ({ row }) => {
        const count = row.original.assigneeCount;
        if (count === 0) return <span style={{ color: 'var(--color-muted)' }}>—</span>;
        return (
          <span
            style={{
              display: 'inline-flex',
              gap: '0.375rem',
              alignItems: 'center',
              color: 'var(--color-muted)',
            }}
          >
            <span
              aria-hidden
              style={{
                inlineSize: '0.5rem',
                blockSize: '0.5rem',
                borderRadius: '999px',
                background: 'var(--nf-color-accent, #9b59b6)',
              }}
            />
            {t('tasks.assignee.count', { count })}
          </span>
        );
      },
    },
    {
      id: 'due',
      accessorKey: 'dueOn',
      size: 100,
      header: () => t('tasks.columns.due'),
      cell: ({ row }) => {
        const dueOn = row.original.dueOn;
        const overdue =
          isOverdue(dueOn) &&
          row.original.derivedState !== 'done' &&
          row.original.derivedState !== 'cancelled';
        return (
          <span
            style={{
              color: overdue ? 'var(--nf-color-danger, #c0392b)' : undefined,
              fontWeight: overdue ? 600 : undefined,
            }}
          >
            {dueOn ?? '—'}
          </span>
        );
      },
    },
    {
      id: 'priority',
      accessorKey: 'priority',
      size: 90,
      header: () => t('tasks.columns.priority'),
      cell: ({ row }) => {
        const p = (row.original.priority as TaskPriority) ?? 0;
        const color = PRIORITY_COLOR[p] ?? PRIORITY_COLOR[0];
        return (
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.375rem',
              fontSize: '0.8125rem',
            }}
          >
            <span
              aria-hidden
              style={{
                width: '0.375rem',
                height: '0.75rem',
                borderRadius: '0.125rem',
                background: color,
                flexShrink: 0,
              }}
            />
            {t(PRIORITY_KEY[p] ?? 'tasks.priority.none')}
          </span>
        );
      },
    },
    {
      id: 'updated',
      accessorKey: 'updatedAt',
      size: 110,
      header: () => t('tasks.columns.updated'),
      cell: ({ row }) => (
        <span style={{ color: 'var(--color-muted)' }}>
          {row.original.updatedAt ? formatDate(row.original.updatedAt, locale) : '—'}
        </span>
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
