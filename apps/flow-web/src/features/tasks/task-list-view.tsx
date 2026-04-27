/**
 * TaskListView — DataGrid view of tasks for a project.
 *
 * Columns: title, status, deps, assignee, due date, priority, updated.
 * Features: column sorting, priority color indicators, status badges,
 * overdue date highlighting, row selection with bulk action toolbar,
 * inline editing for title (double-click), priority (click), and due date (click).
 */

import Button from '@nodate-flow/ui/primitives/button';
import DataGrid from '@nodate-flow/ui/primitives/data-grid';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link, useNavigate } from '@tanstack/react-router';
import type { ColumnDef, RowSelectionState } from '@tanstack/react-table';
import { type DragEvent, type ReactElement, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDate, formatEpoch, isOverdue } from '../../lib/format';
import { computeBlockedByOpen, useProjectDependenciesQuery } from '../projects/api';

import {
  type TaskDerivedState,
  type TaskListItem,
  type TaskPriority,
  useDeleteTask,
  useReorderTasks,
  useTasksInfiniteQuery,
  useUpdateTask,
} from './api';
import { PRIORITY_COLOR, PRIORITY_KEY, STATE_COLOR, STATE_KEY } from './constants';
import { useInlineEdit } from './use-inline-edit';
import { useTaskFilters } from './use-task-filters';

export interface TaskListViewProps {
  projectId: string;
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
        const results = await Promise.allSettled(
          selectedIds.map((id) => updateTask.mutateAsync({ id, patch: { priority } })),
        );
        const failed = results.filter((r) => r.status === 'rejected').length;
        if (failed === 0) {
          toaster.show({ tone: 'success', message: t('tasks.bulk.priority_updated') });
          onClear();
        } else if (failed === results.length) {
          toaster.show({ tone: 'danger', message: t('tasks.bulk.update_failed') });
        } else {
          toaster.show({
            tone: 'danger',
            message: t('tasks.bulk.update_partial', { failed, total: results.length }),
          });
        }
      } finally {
        setBusy(false);
      }
    },
    [selectedIds, updateTask, onClear, t],
  );

  const handleBulkDelete = useCallback(async () => {
    setBusy(true);
    try {
      const results = await Promise.allSettled(selectedIds.map((id) => deleteTask.mutateAsync(id)));
      const failed = results.filter((r) => r.status === 'rejected').length;
      if (failed === 0) {
        toaster.show({ tone: 'success', message: t('tasks.bulk.deleted') });
        onClear();
      } else if (failed === results.length) {
        toaster.show({ tone: 'danger', message: t('tasks.bulk.delete_failed') });
      } else {
        toaster.show({
          tone: 'danger',
          message: t('tasks.bulk.delete_partial', { failed, total: results.length }),
        });
      }
    } finally {
      setBusy(false);
    }
  }, [selectedIds, deleteTask, onClear, t]);

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--nf-space-2)',
        padding: 'var(--nf-space-2) var(--nf-space-3)',
        borderRadius: 'var(--nf-radius-md)',
        background: 'var(--nf-color-accent)',
        color: 'var(--nf-color-fg-on-accent)',
        fontSize: 'var(--nf-text-sm)',
        marginBottom: 'var(--nf-space-2)',
      }}
    >
      <span style={{ fontWeight: 600 }}>
        {t('tasks.bulk.selected', { count: selectedIds.length })}
      </span>
      <span style={{ flex: 1 }} />

      <Select
        aria-label={t('tasks.bulk.set_priority')}
        disabled={busy}
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
      </Select>

      <Button
        type="button"
        variant="danger"
        size="sm"
        disabled={busy}
        aria-busy={busy}
        onClick={() => void handleBulkDelete()}
      >
        {t('tasks.bulk.delete')}
      </Button>

      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={busy}
        aria-busy={busy}
        onClick={onClear}
      >
        {t('tasks.bulk.clear')}
      </Button>
    </div>
  );
}

/* ── Inline edit cells ─────────────────────────────────────── */

function InlineTitleCell({
  task,
  editing,
  onStartEdit,
  onStopEdit,
  onSave,
  onNavigate,
}: {
  task: TaskListItem;
  editing: boolean;
  onStartEdit: () => void;
  onStopEdit: () => void;
  onSave: (title: string) => void;
  onNavigate: () => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const inputRef = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(task.title);

  /* Reset draft when entering edit mode via a re-render. */
  const prevEditing = useRef(false);
  if (editing && !prevEditing.current) {
    setDraft(task.title);
  }
  prevEditing.current = editing;

  /* Auto-focus when switching to edit mode. */
  if (editing && inputRef.current && document.activeElement !== inputRef.current) {
    requestAnimationFrame(() => inputRef.current?.focus());
  }

  if (editing) {
    return (
      <Input
        ref={inputRef}
        aria-label={t('tasks.inline.edit_title')}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            const trimmed = draft.trim();
            if (trimmed.length > 0 && trimmed !== task.title) {
              onSave(trimmed);
            }
            onStopEdit();
          } else if (e.key === 'Escape') {
            onStopEdit();
          }
        }}
        onBlur={() => {
          const trimmed = draft.trim();
          if (trimmed.length > 0 && trimmed !== task.title) {
            onSave(trimmed);
          }
          onStopEdit();
        }}
        style={{
          width: '100%',
          fontSize: 'inherit',
          fontWeight: 500,
          padding: '0.125rem 0.25rem',
          margin: '-0.125rem -0.25rem',
        }}
      />
    );
  }

  /* Distinguish single click (navigate) from double click (edit).
     We delay navigation by 250ms; a double-click within that window
     cancels the pending navigate and enters edit mode instead. */
  const clickTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clean up pending click timer on unmount to prevent stale navigation.
  useEffect(() => {
    return () => {
      if (clickTimer.current !== null) {
        clearTimeout(clickTimer.current);
      }
    };
  }, []);

  return (
    <Link
      to="/tasks/$taskId"
      params={{ taskId: task.id }}
      onClick={(e) => {
        e.preventDefault();
        if (clickTimer.current !== null) return;
        clickTimer.current = setTimeout(() => {
          clickTimer.current = null;
          onNavigate();
        }, 250);
      }}
      onDoubleClick={() => {
        if (clickTimer.current !== null) {
          clearTimeout(clickTimer.current);
          clickTimer.current = null;
        }
        onStartEdit();
      }}
      style={{
        color: 'var(--nf-color-fg)',
        textDecoration: 'none',
        fontWeight: 500,
      }}
    >
      {task.title}
    </Link>
  );
}

function InlinePriorityCell({
  task,
  editing,
  onStartEdit,
  onStopEdit,
  onSave,
}: {
  task: TaskListItem;
  editing: boolean;
  onStartEdit: () => void;
  onStopEdit: () => void;
  onSave: (priority: TaskPriority) => void;
}): ReactElement {
  const { t } = useTranslation('common');
  const selectRef = useRef<HTMLSelectElement>(null);
  const p = (task.priority as TaskPriority) ?? 0;
  const color = PRIORITY_COLOR[p] ?? PRIORITY_COLOR[0];

  /* Auto-focus and open when switching to edit mode. */
  if (editing && selectRef.current && document.activeElement !== selectRef.current) {
    requestAnimationFrame(() => selectRef.current?.focus());
  }

  if (editing) {
    return (
      <Select
        ref={selectRef}
        aria-label={t('tasks.inline.edit_priority')}
        value={String(p)}
        onChange={(e) => {
          const next = Number(e.target.value) as TaskPriority;
          if (next !== p) {
            onSave(next);
          }
          onStopEdit();
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            onStopEdit();
          }
        }}
        onBlur={() => onStopEdit()}
        style={{
          width: '100%',
          fontSize: '0.8125rem',
          padding: '0.125rem 0.25rem',
          margin: '-0.125rem -0.25rem',
        }}
      >
        <option value="0">{t('tasks.priority.none')}</option>
        <option value="1">{t('tasks.priority.low')}</option>
        <option value="2">{t('tasks.priority.medium')}</option>
        <option value="3">{t('tasks.priority.high')}</option>
        <option value="4">{t('tasks.priority.urgent')}</option>
      </Select>
    );
  }

  const label = t(PRIORITY_KEY[p] ?? 'tasks.priority.none');
  return (
    <span
      role="button"
      tabIndex={0}
      title={t('tasks.inline.edit_priority')}
      aria-label={`${label} — ${t('tasks.inline.edit_priority')}`}
      onClick={onStartEdit}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onStartEdit();
        }
      }}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.375rem',
        fontSize: '0.8125rem',
        cursor: 'pointer',
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
      {label}
    </span>
  );
}

function InlineDueCell({
  task,
  editing,
  onStartEdit,
  onStopEdit,
  onSave,
  locale,
}: {
  task: TaskListItem;
  editing: boolean;
  onStartEdit: () => void;
  onStopEdit: () => void;
  onSave: (dueOn: string) => void;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const weekdayLabels = t('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const dueOn = task.dueOn;
  const overdue =
    isOverdue(dueOn) && task.derivedState !== 'done' && task.derivedState !== 'cancelled';

  if (editing) {
    return (
      <DatePicker
        value={dueOn ?? ''}
        onChange={(next) => {
          if (next !== (dueOn ?? '')) {
            onSave(next);
          }
          onStopEdit();
        }}
        weekdayLabels={weekdayLabels}
        formatMonthYear={formatMonthYear}
        prevLabel={t('calendar.prev')}
        nextLabel={t('calendar.next')}
        triggerLabel={dueOn ? formatDate(dueOn, locale) : t('common.date.placeholder')}
      />
    );
  }

  const displayDate = dueOn ? formatDate(dueOn, locale) : '—';
  const ariaLabel = dueOn
    ? t('tasks.inline.edit_due_set', { date: formatDate(dueOn, locale) })
    : t('tasks.inline.edit_due_unset');
  return (
    <span
      role="button"
      tabIndex={0}
      title={t('tasks.inline.edit_due')}
      aria-label={ariaLabel}
      onClick={onStartEdit}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onStartEdit();
        }
      }}
      style={{
        color: overdue ? 'var(--nf-color-danger)' : undefined,
        fontWeight: overdue ? 600 : undefined,
        cursor: 'pointer',
      }}
    >
      {displayDate}
    </span>
  );
}

/* ── Main list view ─────────────────────────────────────────── */

export default function TaskListView({ projectId }: TaskListViewProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const filters = useTaskFilters(projectId);
  // Cursor-paginated infinite list. We flat-map pages here — the order is
  // stable because the cursor is keyset (created_at, id) on the server.
  const { data, hasNextPage, isFetchingNextPage, fetchNextPage } = useTasksInfiniteQuery(
    projectId,
    filters,
  );
  const tasks = data.pages.flatMap((p) => p.tasks);
  const { data: edges } = useProjectDependenciesQuery(projectId);
  const blockedByOpen = computeBlockedByOpen(edges);
  const locale = i18n.resolvedLanguage ?? 'en';
  const updateTask = useUpdateTask();
  const reorderTasks = useReorderTasks();
  const navigate = useNavigate();
  const inlineEdit = useInlineEdit();
  const gridRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);

  // Auto-fetch the next page when the sentinel below the grid enters the
  // grid's own scroll viewport. The sentinel is placed inside the same
  // scroll container so a stable IntersectionObserver root suffices.
  useEffect(() => {
    if (!hasNextPage) return;
    const root = gridRef.current;
    const sentinel = sentinelRef.current;
    if (!root || !sentinel) return;
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && hasNextPage && !isFetchingNextPage) {
            void fetchNextPage();
          }
        }
      },
      { root, rootMargin: '200px 0px', threshold: 0 },
    );
    observer.observe(sentinel);
    return () => {
      observer.disconnect();
    };
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  const handleLoadMore = useCallback((): void => {
    if (!hasNextPage || isFetchingNextPage) return;
    void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});

  /* ── DnD row reorder state ──────────────────────────────────── */
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [dropIdx, setDropIdx] = useState<number | null>(null);

  const handleDragStart = useCallback((e: DragEvent, idx: number) => {
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(idx));
    setDragIdx(idx);
  }, []);

  const handleDragOver = useCallback(
    (e: DragEvent, idx: number) => {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      if (idx !== dropIdx) setDropIdx(idx);
    },
    [dropIdx],
  );

  const handleDragEnd = useCallback(() => {
    setDragIdx(null);
    setDropIdx(null);
  }, []);

  const handleDragLeave = useCallback(() => {
    setDropIdx(null);
  }, []);

  const handleDrop = useCallback(
    (e: DragEvent, targetIdx: number) => {
      e.preventDefault();
      const sourceIdx = Number(e.dataTransfer.getData('text/plain'));
      setDragIdx(null);
      setDropIdx(null);
      if (Number.isNaN(sourceIdx) || sourceIdx === targetIdx) return;
      if (tasks.length < 2) return;

      // Build the new order by moving source to target position
      const reordered = [...tasks];
      const [moved] = reordered.splice(sourceIdx, 1);
      if (!moved) return;
      reordered.splice(targetIdx, 0, moved);

      // Assign sequential sort weights (gap of 1000 for future inserts)
      const items = reordered.map((task, i) => ({
        id: task.id,
        sortWeight: (i + 1) * 1000,
      }));

      void reorderTasks.mutateAsync({ projectId, items }).catch(() => {
        toaster.show({ tone: 'danger', message: t('tasks.reorder.failed') });
      });
    },
    [tasks, projectId, reorderTasks, t],
  );

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

  const handleInlineSave = (
    id: string,
    patch: { title?: string; priority?: TaskPriority; dueOn?: string },
  ) => {
    void updateTask.mutateAsync({ id, patch }).catch(() => {
      toaster.show({ tone: 'danger', message: t('tasks.inline.save_failed') });
    });
  };

  const columns: ColumnDef<TaskListItem, unknown>[] = [
    {
      id: 'drag',
      size: 32,
      enableSorting: false,
      header: () => null,
      cell: ({ row }) => (
        <span
          draggable
          aria-label={t('tasks.reorder.drag_handle')}
          title={t('tasks.reorder.drag_handle')}
          onDragStart={(e) => handleDragStart(e, row.index)}
          onDragOver={(e) => handleDragOver(e, row.index)}
          onDragLeave={handleDragLeave}
          onDragEnd={handleDragEnd}
          onDrop={(e) => handleDrop(e, row.index)}
          style={{
            cursor: 'grab',
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            width: '100%',
            userSelect: 'none',
            opacity: dragIdx === row.index ? 0.4 : 1,
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-xs)',
            lineHeight: 1,
          }}
        >
          ⠿
        </span>
      ),
    },
    {
      id: 'title',
      accessorKey: 'title',
      size: 320,
      header: () => t('tasks.columns.title'),
      cell: ({ row }) => (
        <InlineTitleCell
          task={row.original}
          editing={inlineEdit.isEditing(row.original.id, 'title')}
          onStartEdit={() => inlineEdit.startEdit(row.original.id, 'title')}
          onStopEdit={inlineEdit.stopEdit}
          onSave={(title) => handleInlineSave(row.original.id, { title })}
          onNavigate={() =>
            void navigate({ to: '/tasks/$taskId', params: { taskId: row.original.id } })
          }
        />
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
              gap: '0.25rem',
              fontSize: 'var(--nf-text-xs)',
              fontWeight: 500,
              padding: '0.125rem 0.5rem',
              borderRadius: '999px',
              background: `${color}18`,
              color: color,
              whiteSpace: 'nowrap',
            }}
          >
            <span
              aria-hidden
              style={{
                width: '0.375rem',
                height: '0.375rem',
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
        if (count === 0) return <span style={{ color: 'var(--nf-color-fg-muted)' }}>—</span>;
        return (
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.25rem',
              color: 'var(--nf-color-danger)',
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
        if (count === 0) return <span style={{ color: 'var(--nf-color-fg-muted)' }}>—</span>;
        return (
          <span
            style={{
              display: 'inline-flex',
              gap: '0.375rem',
              alignItems: 'center',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            <span
              aria-hidden
              style={{
                inlineSize: '0.5rem',
                blockSize: '0.5rem',
                borderRadius: '999px',
                background: 'var(--nf-color-accent)',
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
      cell: ({ row }) => (
        <InlineDueCell
          task={row.original}
          editing={inlineEdit.isEditing(row.original.id, 'due')}
          onStartEdit={() => inlineEdit.startEdit(row.original.id, 'due')}
          onStopEdit={inlineEdit.stopEdit}
          onSave={(dueOn) => handleInlineSave(row.original.id, { dueOn })}
          locale={locale}
        />
      ),
    },
    {
      id: 'priority',
      accessorKey: 'priority',
      size: 90,
      header: () => t('tasks.columns.priority'),
      cell: ({ row }) => (
        <InlinePriorityCell
          task={row.original}
          editing={inlineEdit.isEditing(row.original.id, 'priority')}
          onStartEdit={() => inlineEdit.startEdit(row.original.id, 'priority')}
          onStopEdit={inlineEdit.stopEdit}
          onSave={(priority) => handleInlineSave(row.original.id, { priority })}
        />
      ),
    },
    {
      id: 'updated',
      accessorKey: 'updatedAt',
      size: 110,
      header: () => t('tasks.columns.updated'),
      cell: ({ row }) => (
        <span style={{ color: 'var(--nf-color-fg-muted)' }}>
          {formatEpoch(row.original.updatedAt, locale) ?? '—'}
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
        ref={gridRef}
        aria-label={t('tasks.title')}
        columns={columns}
        data={tasks}
        emptyContent={t('tasks.empty')}
        enableRowSelection
        rowSelection={rowSelection}
        onRowSelectionChange={setRowSelection}
        selectAllRowsLabel={t('tasks.list.select_all')}
        selectRowLabel={(index) => t('tasks.list.select_row', { index })}
        style={{ minBlockSize: '20rem' }}
      />
      {/*
        Sentinel + manual fallback. The IntersectionObserver above watches
        this div inside the grid's own scroll viewport so we trigger the
        next page when the user scrolls within ~200px of the end.
        The button is shown as a fallback for keyboard users / when the
        observer doesn't fire (e.g. very short lists).
      */}
      {hasNextPage ? (
        <div
          style={{
            display: 'flex',
            justifyContent: 'center',
            padding: 'var(--nf-space-3)',
          }}
        >
          <div
            ref={sentinelRef}
            aria-hidden="true"
            style={{ inlineSize: '100%', blockSize: '1px' }}
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={isFetchingNextPage}
            aria-busy={isFetchingNextPage}
            onClick={handleLoadMore}
          >
            {isFetchingNextPage ? t('tasks.list.loading_more') : t('tasks.list.load_more')}
          </Button>
        </div>
      ) : null}
    </>
  );
}
