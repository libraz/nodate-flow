/**
 * TaskListView — DataGrid view of tasks for a project.
 *
 * Columns: title, status, deps, assignee, due date, priority, updated.
 * Features: column sorting, priority color indicators, status badges,
 * overdue date highlighting, row selection with bulk action toolbar.
 */

import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import type { ColumnDef, RowSelectionState } from '@tanstack/react-table';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { computeBlockedByOpen, useProjectDependenciesQuery } from '../projects/api';

import {
  type TaskDerivedState,
  type TaskListItem,
  type TaskPriority,
  useDeleteTask,
  useTasksQuery,
  useUpdateTask,
} from './api';
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

/* ── Bulk action toolbar ────────────────────────────────────── */

function BulkActionBar({
  selectedIds,
  onClear,
}: {
  selectedIds: string[];
  onClear: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const updateTask = useUpdateTask();
  const deleteTask = useDeleteTask();
  const [busy, setBusy] = useState(false);

  const handleBulkPriority = useCallback(
    async (priority: TaskPriority) => {
      setBusy(true);
      try {
        await Promise.all(
          selectedIds.map((id) => updateTask.mutateAsync({ id, patch: { priority } })),
        );
        toaster.show({ tone: 'success', message: t('tasks.bulk.priority_updated') });
        onClear();
      } catch {
        toaster.show({ tone: 'danger', message: t('tasks.bulk.update_failed') });
      } finally {
        setBusy(false);
      }
    },
    [selectedIds, updateTask, onClear, t],
  );

  const handleBulkDelete = useCallback(async () => {
    setBusy(true);
    try {
      await Promise.all(selectedIds.map((id) => deleteTask.mutateAsync(id)));
      toaster.show({ tone: 'success', message: t('tasks.bulk.deleted') });
      onClear();
    } catch {
      toaster.show({ tone: 'danger', message: t('tasks.bulk.delete_failed') });
    } finally {
      setBusy(false);
    }
  }, [selectedIds, deleteTask, onClear, t]);

  const btnStyle: React.CSSProperties = {
    padding: '0.375rem 0.75rem',
    borderRadius: '0.375rem',
    border: '1px solid var(--nf-color-border, var(--color-hairline))',
    background: 'var(--color-surface, rgba(127,127,127,0.05))',
    color: 'var(--color-fg)',
    fontSize: '0.8125rem',
    cursor: busy ? 'wait' : 'pointer',
    opacity: busy ? 0.5 : 1,
  };

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
        padding: '0.5rem 0.75rem',
        borderRadius: '0.5rem',
        background: 'var(--nf-color-accent, var(--color-accent, #9b59b6))',
        color: 'var(--nf-color-accent-fg, white)',
        fontSize: '0.8125rem',
        marginBottom: '0.5rem',
      }}
    >
      <span style={{ fontWeight: 600 }}>
        {t('tasks.bulk.selected', { count: selectedIds.length })}
      </span>
      <span style={{ flex: 1 }} />

      <select
        aria-label={t('tasks.bulk.set_priority')}
        disabled={busy}
        style={{
          ...btnStyle,
          appearance: 'auto',
        }}
        defaultValue=""
        onChange={(e) => {
          if (e.target.value) {
            void handleBulkPriority(Number(e.target.value) as TaskPriority);
            e.target.value = '';
          }
        }}
      >
        <option value="" disabled>
          {t('tasks.bulk.set_priority')}
        </option>
        <option value="0">{t('tasks.priority.none')}</option>
        <option value="1">{t('tasks.priority.low')}</option>
        <option value="2">{t('tasks.priority.medium')}</option>
        <option value="3">{t('tasks.priority.high')}</option>
        <option value="4">{t('tasks.priority.urgent')}</option>
      </select>

      <button
        type="button"
        disabled={busy}
        style={{ ...btnStyle, color: 'var(--nf-color-danger, #c0392b)' }}
        onClick={() => void handleBulkDelete()}
      >
        {t('tasks.bulk.delete')}
      </button>

      <button
        type="button"
        disabled={busy}
        style={{ ...btnStyle, border: 'none', background: 'transparent', color: 'inherit' }}
        onClick={onClear}
      >
        {t('tasks.bulk.clear')}
      </button>
    </div>
  );
}

/* ── Main list view ─────────────────────────────────────────── */

export default function TaskListView({ projectId }: TaskListViewProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const { data: edges } = useProjectDependenciesQuery(projectId);
  const blockedByOpen = computeBlockedByOpen(edges);
  const locale = i18n.resolvedLanguage ?? 'en';

  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});

  const selectedIds = Object.keys(rowSelection)
    .filter((k) => rowSelection[k])
    .map((idx) => {
      const task = tasks[Number(idx)];
      return task?.id ?? '';
    })
    .filter(Boolean);

  const handleClearSelection = useCallback(() => {
    setRowSelection({});
  }, []);

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
    <>
      {selectedIds.length > 0 && (
        <BulkActionBar selectedIds={selectedIds} onClear={handleClearSelection} />
      )}
      <DataGrid<TaskListItem>
        aria-label={t('tasks.title')}
        columns={columns}
        data={tasks}
        emptyContent={t('tasks.empty')}
        enableRowSelection
        rowSelection={rowSelection}
        onRowSelectionChange={setRowSelection}
        style={{ minBlockSize: '20rem' }}
      />
    </>
  );
}
