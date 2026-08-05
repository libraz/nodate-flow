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
import {
  type DragEvent,
  type KeyboardEvent,
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { formatDate, formatEpoch, isOverdue } from '../../lib/format';
import { useWeekStart } from '../../lib/use-week-start';
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
import {
  PRIORITY_COLOR,
  PRIORITY_KEY,
  STATE_COLOR,
  STATE_KEY,
  STATE_TEXT_COLOR,
} from './constants';
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
          const first = results.find((r) => r.status === 'rejected');
          toaster.show({
            tone: 'danger',
            message: formatApiError(first?.reason, t, 'tasks.bulk.update_failed'),
          });
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
        const first = results.find((r) => r.status === 'rejected');
        toaster.show({
          tone: 'danger',
          message: formatApiError(first?.reason, t, 'tasks.bulk.delete_failed'),
        });
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
  /* Distinguish single click (navigate) from double click (edit).
     We delay navigation by 250ms; a double-click within that window
     cancels the pending navigate and enters edit mode instead. */
  const clickTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  /* Reset draft and focus when switching to edit mode. */
  useEffect(() => {
    if (!editing) return;
    setDraft(task.title);
    requestAnimationFrame(() => {
      if (inputRef.current && document.activeElement !== inputRef.current) {
        inputRef.current.focus();
      }
    });
  }, [editing, task.title]);

  // Clean up pending click timer on unmount to prevent stale navigation.
  useEffect(() => {
    return () => {
      if (clickTimer.current !== null) {
        clearTimeout(clickTimer.current);
      }
    };
  }, []);

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
          padding: 'var(--nf-space-0-5) var(--nf-space-1)',
          margin: 'calc(var(--nf-space-0-5) * -1) calc(var(--nf-space-1) * -1)',
        }}
      />
    );
  }

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
          fontSize: 'var(--nf-text-supporting)',
          padding: 'var(--nf-space-0-5) var(--nf-space-1)',
          margin: 'calc(var(--nf-space-0-5) * -1) calc(var(--nf-space-1) * -1)',
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
        gap: 'var(--nf-space-1-5)',
        fontSize: 'var(--nf-text-supporting)',
        cursor: 'pointer',
      }}
    >
      <span
        aria-hidden
        style={{
          // nf-token-override: component dimension, not a spacing step
          width: '0.375rem',
          // nf-token-override: component dimension, not a spacing step
          height: '0.75rem',
          // nf-token-override: the corner of a 6x12px colour swatch. --nf-radius-xs spans 0 to 6px across the themes, which on a swatch this small is the difference between a square and a lozenge; a data legend has to read the same in every theme
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
  onSave: (dueOn: string | null) => void;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('common');
  const weekStart = useWeekStart();
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
        weekStart={weekStart}
        formatMonthYear={formatMonthYear}
        prevLabel={t('calendar.prev')}
        nextLabel={t('calendar.next')}
        triggerLabel={dueOn ? formatDate(dueOn, locale) : t('common.date.placeholder')}
        onClear={() => {
          if (dueOn) {
            onSave(null);
          }
          onStopEdit();
        }}
        clearLabel={t('common.date.clear')}
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
        color: overdue ? 'var(--nf-color-danger-fg)' : undefined,
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
  // OFFSET-paginated infinite list. We flat-map pages here; the project list
  // intentionally stays on the backend sort_weight order so DnD reorder works
  // beyond the first fetched page.
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
  const selectionResetKey = [
    filters.search ?? '',
    filters.assigneeId ?? '',
    (filters.states ?? []).join(','),
    (filters.priority ?? []).join(','),
  ].join('\0');
  const previousSelectionResetKey = useRef(selectionResetKey);

  useEffect(() => {
    if (previousSelectionResetKey.current === selectionResetKey) return;
    previousSelectionResetKey.current = selectionResetKey;
    setRowSelection({});
  }, [selectionResetKey]);

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

  const reorderRows = useCallback(
    (sourceIdx: number, targetIdx: number) => {
      if (Number.isNaN(sourceIdx) || sourceIdx === targetIdx) return;
      if (sourceIdx < 0 || targetIdx < 0 || sourceIdx >= tasks.length || targetIdx >= tasks.length)
        return;
      if (tasks.length < 2) return;

      const reordered = [...tasks];
      const [moved] = reordered.splice(sourceIdx, 1);
      if (!moved) return;
      reordered.splice(targetIdx, 0, moved);

      const items = reordered.map((task, i) => ({
        id: task.id,
        sortWeight: (i + 1) * 1000,
      }));

      void reorderTasks.mutateAsync({ projectId, items }).catch((err) => {
        toaster.show({ tone: 'danger', message: formatApiError(err, t, 'tasks.reorder.failed') });
      });
    },
    [tasks, projectId, reorderTasks, t],
  );

  const handleDrop = useCallback(
    (e: DragEvent, targetIdx: number) => {
      e.preventDefault();
      const sourceIdx = Number(e.dataTransfer.getData('text/plain'));
      setDragIdx(null);
      setDropIdx(null);
      reorderRows(sourceIdx, targetIdx);
    },
    [reorderRows],
  );

  const handleReorderKeyDown = useCallback(
    (e: KeyboardEvent, sourceIdx: number) => {
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        reorderRows(sourceIdx, sourceIdx - 1);
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        reorderRows(sourceIdx, sourceIdx + 1);
      }
    },
    [reorderRows],
  );

  const selectedIds = Object.keys(rowSelection).filter((id) => rowSelection[id]);

  const handleClearSelection = useCallback(() => {
    setRowSelection({});
  }, []);

  const handleInlineSave = useCallback(
    (id: string, patch: { title?: string; priority?: TaskPriority; dueOn?: string | null }) => {
      // Backend `*string` treats "" as clear and null as unchanged; map at the wire.
      const wirePatch: { title?: string; priority?: TaskPriority; dueOn?: string } = {
        ...(patch.title !== undefined && { title: patch.title }),
        ...(patch.priority !== undefined && { priority: patch.priority }),
        ...(patch.dueOn !== undefined && { dueOn: patch.dueOn ?? '' }),
      };
      void updateTask.mutateAsync({ id, patch: wirePatch }).catch((err) => {
        toaster.show({
          tone: 'danger',
          message: formatApiError(err, t, 'tasks.inline.save_failed'),
        });
      });
    },
    [updateTask, t],
  );

  /*
   * A new array on every render makes TanStack Table rebuild its column
   * model on every render, for a grid that carries the whole task list.
   *
   * Written by hand because there is nothing to write it for us: the
   * frontend convention bans useMemo on the grounds that React Compiler
   * memoises automatically, but the compiler is not installed — neither
   * app's vite config passes it to the react plugin, and neither
   * babel-plugin-react-compiler nor eslint-plugin-react-compiler is a
   * dependency. If it is ever adopted, this memo becomes redundant and
   * can go.
   *
   * The dependency list is every value the cells close over. It is long
   * because the cells are rich, not because the memo is doing something
   * unusual.
   */
  const columns: ColumnDef<TaskListItem, unknown>[] = useMemo(
    () => [
      {
        id: 'drag',
        size: 32,
        enableSorting: false,
        header: () => null,
        cell: ({ row }) => (
          <span
            draggable
            role="button"
            tabIndex={0}
            aria-label={t('tasks.reorder.drag_handle')}
            title={t('tasks.reorder.drag_handle')}
            onDragStart={(e) => handleDragStart(e, row.index)}
            onDragOver={(e) => handleDragOver(e, row.index)}
            onDragLeave={handleDragLeave}
            onDragEnd={handleDragEnd}
            onDrop={(e) => handleDrop(e, row.index)}
            onKeyDown={(e) => handleReorderKeyDown(e, row.index)}
            aria-keyshortcuts="ArrowUp ArrowDown"
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
          const textColor = STATE_TEXT_COLOR[state] ?? STATE_TEXT_COLOR.open;
          return (
            <span
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 'var(--nf-space-1)',
                fontSize: 'var(--nf-text-xs)',
                fontWeight: 500,
                padding: 'var(--nf-space-0-5) var(--nf-space-2)',
                borderRadius: 'var(--nf-radius-pill)',
                // Appending an alpha suffix to `var(--x)` produces
                // `var(--x)18`, which is not a colour: the declaration was
                // dropped and the pill had no wash at all.
                background: `color-mix(in oklab, ${color} 9%, transparent)`,
                color: textColor,
                whiteSpace: 'nowrap',
              }}
            >
              <span
                aria-hidden
                style={{
                  // nf-token-override: component dimension, not a spacing step
                  width: '0.375rem',
                  // nf-token-override: component dimension, not a spacing step
                  height: '0.375rem',
                  borderRadius: 'var(--nf-radius-pill)',
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
                gap: 'var(--nf-space-1)',
                color: 'var(--nf-color-danger-fg)',
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
                gap: 'var(--nf-space-1-5)',
                alignItems: 'center',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              <span
                aria-hidden
                style={{
                  // nf-token-override: component dimension, not a spacing step
                  inlineSize: '0.5rem',
                  // nf-token-override: component dimension, not a spacing step
                  blockSize: '0.5rem',
                  borderRadius: 'var(--nf-radius-pill)',
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
    ],
    [
      t,
      locale,
      navigate,
      inlineEdit,
      blockedByOpen,
      dragIdx,
      handleDragEnd,
      handleDragLeave,
      handleDragOver,
      handleDragStart,
      handleDrop,
      handleInlineSave,
      handleReorderKeyDown,
    ],
  );

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
        getRowId={(row) => row.id}
        emptyContent={t('tasks.empty')}
        enableRowSelection
        rowSelection={rowSelection}
        onRowSelectionChange={setRowSelection}
        selectAllRowsLabel={t('tasks.list.select_all')}
        selectRowLabel={(index) => t('tasks.list.select_row', { index })}
        // nf-token-override: component dimension, not a spacing step
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
