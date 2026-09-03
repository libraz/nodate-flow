/**
 * TaskSpreadsheetView — spreadsheet-style task view with virtualized rows,
 * inline cell editing (click-to-edit, Tab/Shift+Tab navigation, Enter to
 * commit-and-move-down, Escape to cancel), bulk actions, and full keyboard
 * navigation.
 *
 * Uses TanStack Table for column definitions and @tanstack/react-virtual
 * for row virtualization to handle 500+ tasks smoothly.
 */

import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { ReactElement } from 'react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { formatDate, formatEpoch, isOverdue } from '../../lib/format';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import { useWeekStart } from '../../lib/use-week-start';
import {
  TASKS_QUERY_LIMIT,
  type TaskDerivedState,
  type TaskListItem,
  type TaskPriority,
  useDeleteTask,
  useTasksQuery,
  useUpdateTask,
} from './api';
import { PRIORITY_COLOR, PRIORITY_KEY, STATE_COLOR, STATE_KEY } from './constants';
import css from './task-spreadsheet-view.module.css';
import { useTaskFilters } from './use-task-filters';

export interface TaskSpreadsheetViewProps {
  projectId: string;
}

/* ── Constants ─────────────────────────────────────────────── */

const ROW_HEIGHT = 36;

const EDITABLE_COLUMNS = ['title', 'status', 'priority', 'assignee', 'due'] as const;
type EditableColumn = (typeof EDITABLE_COLUMNS)[number];

interface CellAddress {
  rowIdx: number;
  taskId: string;
  column: EditableColumn;
}

function nextEditableColumn(col: EditableColumn, direction: 1 | -1): EditableColumn | null {
  const idx = EDITABLE_COLUMNS.indexOf(col);
  const next = idx + direction;
  return EDITABLE_COLUMNS[next] ?? null;
}

/* ── Bulk action toolbar ──────────────────────────────────── */

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

  const handleBulkPriority = async (priority: TaskPriority) => {
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
  };

  const handleBulkDelete = async () => {
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
  };

  return (
    <div className={css.bulkBar}>
      <span className={css.bulkBarCount}>
        {t('tasks.spreadsheet.selected_count', { count: selectedIds.length })}
      </span>
      <span className={css.bulkBarSpacer} />

      <select
        aria-label={t('tasks.spreadsheet.set_priority')}
        disabled={busy}
        className={css.bulkBtn}
        defaultValue=""
        onChange={(e) => {
          if (e.target.value) {
            void handleBulkPriority(Number(e.target.value) as TaskPriority);
            e.target.value = '';
          }
        }}
      >
        <option value="" disabled>
          {t('tasks.spreadsheet.set_priority')}
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
        className={css.bulkBtnDanger}
        onClick={() => void handleBulkDelete()}
      >
        {t('tasks.bulk.delete')}
      </button>

      <button type="button" disabled={busy} className={css.bulkBtnClear} onClick={onClear}>
        {t('tasks.bulk.clear')}
      </button>
    </div>
  );
}

/* ── Main spreadsheet view ────────────────────────────────── */

export default function TaskSpreadsheetView({ projectId }: TaskSpreadsheetViewProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const weekStart = useWeekStart();
  const zone = useEffectiveZone();
  const filters = useTaskFilters(projectId);
  const { data: tasks } = useTasksQuery(projectId, filters);
  const locale = i18n.resolvedLanguage ?? 'en';
  const weekdayLabels = t('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    t('common.date.monthYear', { year, month });
  const updateTask = useUpdateTask();

  const [selectedRows, setSelectedRows] = useState<Set<string>>(new Set());
  const [activeCell, setActiveCell] = useState<CellAddress | null>(null);
  const [editingCell, setEditingCell] = useState<CellAddress | null>(null);
  const [editDraft, setEditDraft] = useState('');

  const parentRef = useRef<HTMLDivElement>(null);
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
    setSelectedRows(new Set());
  }, [selectionResetKey]);

  const virtualizer = useVirtualizer({
    count: tasks.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 10,
  });

  /* ── Selection helpers ─────────────────────────────────────── */

  const toggleRow = (id: string) => {
    setSelectedRows((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const toggleAll = () => {
    if (selectedRows.size === tasks.length) {
      setSelectedRows(new Set());
    } else {
      setSelectedRows(new Set(tasks.map((task) => task.id)));
    }
  };

  const clearSelection = () => {
    setSelectedRows(new Set());
  };

  const selectedIds = [...selectedRows];
  const mayBeTruncated = tasks.length >= TASKS_QUERY_LIMIT;

  /* ── Edit lifecycle ────────────────────────────────────────── */

  const startEdit = (rowIdx: number, column: EditableColumn) => {
    const task = tasks[rowIdx];
    if (!task) return;

    let draft = '';
    switch (column) {
      case 'title':
        draft = task.title;
        break;
      case 'status':
        draft = (task.derivedState as string) ?? 'open';
        break;
      case 'priority':
        draft = String((task.priority as number) ?? 0);
        break;
      case 'assignee':
        draft = '';
        break;
      case 'due':
        draft = task.dueOn ?? '';
        break;
    }
    setEditDraft(draft);
    setEditingCell({ rowIdx, taskId: task.id, column });
    setActiveCell({ rowIdx, taskId: task.id, column });
  };

  const commitEdit = (rowIdx: number, column: EditableColumn, value: string) => {
    // Look up by the task ID stored when editing started to avoid stale
    // rowIdx if the tasks array was reordered/refreshed mid-edit.
    const editTaskId = editingCell?.column === column ? editingCell.taskId : tasks[rowIdx]?.id;
    const task = editTaskId ? tasks.find((t) => t.id === editTaskId) : tasks[rowIdx];
    if (!task) return;

    let patch: { title?: string; priority?: TaskPriority; dueOn?: string } | null = null;

    switch (column) {
      case 'title': {
        const trimmed = value.trim();
        if (trimmed.length > 0 && trimmed !== task.title) {
          patch = { title: trimmed };
        }
        break;
      }
      case 'priority': {
        const next = Number(value) as TaskPriority;
        if (next !== ((task.priority as TaskPriority) ?? 0)) {
          patch = { priority: next };
        }
        break;
      }
      case 'due': {
        if (value !== (task.dueOn ?? '')) {
          patch = { dueOn: value };
        }
        break;
      }
      // status and assignee: status requires transitions (not simple PATCH),
      // and assignee requires the actors endpoint. For now these are read-only
      // in terms of commit (the dropdown still shows values but the PATCH API
      // does not support derivedState directly).
      default:
        break;
    }

    if (patch) {
      void updateTask.mutateAsync({ id: task.id, patch }).catch((err) => {
        toaster.show({
          tone: 'danger',
          message: formatApiError(err, t, 'tasks.inline.save_failed'),
        });
      });
    }
  };

  const cancelEdit = () => {
    setEditingCell(null);
  };

  const commitAndStop = () => {
    if (editingCell) {
      commitEdit(editingCell.rowIdx, editingCell.column, editDraft);
      setEditingCell(null);
    }
  };

  /* ── Navigation helpers ────────────────────────────────────── */

  const moveToNextCell = (from: CellAddress, direction: 'tab' | 'shift-tab' | 'enter') => {
    if (direction === 'enter') {
      // Move down in same column
      const nextRow = from.rowIdx + 1;
      if (nextRow < tasks.length) {
        startEdit(nextRow, from.column);
      } else {
        setEditingCell(null);
      }
      return;
    }

    const step = direction === 'tab' ? 1 : -1;
    const nextCol = nextEditableColumn(from.column, step);

    if (nextCol) {
      startEdit(from.rowIdx, nextCol);
    } else {
      // Wrap to next/prev row
      const nextRow = from.rowIdx + step;
      if (nextRow >= 0 && nextRow < tasks.length) {
        const wrapCol =
          step === 1 ? EDITABLE_COLUMNS[0] : EDITABLE_COLUMNS[EDITABLE_COLUMNS.length - 1];
        if (wrapCol) {
          startEdit(nextRow, wrapCol);
        }
      } else {
        setEditingCell(null);
      }
    }
  };

  /* ── Cell key handler for editing mode ─────────────────────── */

  const handleCellKeyDown = (e: React.KeyboardEvent) => {
    if (!editingCell) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      cancelEdit();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      commitEdit(editingCell.rowIdx, editingCell.column, editDraft);
      moveToNextCell(editingCell, 'enter');
    } else if (e.key === 'Tab') {
      e.preventDefault();
      commitEdit(editingCell.rowIdx, editingCell.column, editDraft);
      moveToNextCell(editingCell, e.shiftKey ? 'shift-tab' : 'tab');
    }
  };

  /* ── Render helpers ────────────────────────────────────────── */

  const isActive = (rowIdx: number, column: string): boolean =>
    activeCell?.rowIdx === rowIdx && activeCell.column === column;

  const isEditing = (rowIdx: number, column: string): boolean =>
    editingCell?.rowIdx === rowIdx && editingCell.column === column;

  const cellClassName = (rowIdx: number, column: string, editable: boolean): string => {
    const parts: string[] = [css.cell ?? ''];
    if (editable) parts.push(css.cellEditable ?? '');
    if (isActive(rowIdx, column)) parts.push(css.cellActive ?? '');
    if (isEditing(rowIdx, column)) parts.push(css.cellEditing ?? '');
    return parts.filter(Boolean).join(' ');
  };

  const renderTitleCell = (task: TaskListItem, rowIdx: number): ReactElement => {
    if (isEditing(rowIdx, 'title')) {
      return (
        <input
          type="text"
          className={css.cellInput}
          value={editDraft}
          aria-label={t('tasks.inline.edit_title')}
          // biome-ignore lint/a11y/noAutofocus: inline edit cell needs focus
          autoFocus
          onChange={(e) => setEditDraft(e.target.value)}
          onKeyDown={handleCellKeyDown}
          onBlur={commitAndStop}
        />
      );
    }
    return <span>{task.title}</span>;
  };

  const renderStatusCell = (task: TaskListItem, rowIdx: number): ReactElement => {
    const state = (task.derivedState as TaskDerivedState) ?? 'open';
    const color = STATE_COLOR[state] ?? STATE_COLOR.open;

    if (isEditing(rowIdx, 'status')) {
      return (
        <select
          className={css.cellSelect}
          value={editDraft}
          aria-label={t('tasks.inline.edit_status')}
          // biome-ignore lint/a11y/noAutofocus: inline edit cell needs focus
          autoFocus
          onChange={(e) => {
            setEditDraft(e.target.value);
          }}
          onKeyDown={handleCellKeyDown}
          onBlur={() => {
            // Status is read-only for now (requires transitions API)
            cancelEdit();
          }}
        >
          <option value="open">{t('tasks.status.open')}</option>
          <option value="waiting">{t('tasks.status.waiting')}</option>
          <option value="review">{t('tasks.status.review')}</option>
          <option value="done">{t('tasks.status.done')}</option>
          <option value="cancelled">{t('tasks.status.cancelled')}</option>
        </select>
      );
    }

    return (
      <span style={{ display: 'inline-flex', alignItems: 'center' }}>
        <span className={css.statusDot} style={{ background: color }} aria-hidden />
        {t(STATE_KEY[state] ?? 'tasks.status.open')}
      </span>
    );
  };

  const renderPriorityCell = (task: TaskListItem, rowIdx: number): ReactElement => {
    const p = (task.priority as TaskPriority) ?? 0;
    const color = PRIORITY_COLOR[p] ?? PRIORITY_COLOR[0];

    if (isEditing(rowIdx, 'priority')) {
      return (
        <select
          className={css.cellSelect}
          value={editDraft}
          aria-label={t('tasks.inline.edit_priority')}
          // biome-ignore lint/a11y/noAutofocus: inline edit cell needs focus
          autoFocus
          onChange={(e) => {
            setEditDraft(e.target.value);
            commitEdit(rowIdx, 'priority', e.target.value);
            setEditingCell(null);
          }}
          onKeyDown={handleCellKeyDown}
          onBlur={commitAndStop}
        >
          <option value="0">{t('tasks.priority.none')}</option>
          <option value="1">{t('tasks.priority.low')}</option>
          <option value="2">{t('tasks.priority.medium')}</option>
          <option value="3">{t('tasks.priority.high')}</option>
          <option value="4">{t('tasks.priority.urgent')}</option>
        </select>
      );
    }

    return (
      <span style={{ display: 'inline-flex', alignItems: 'center' }}>
        <span className={css.priorityBar} style={{ background: color }} aria-hidden />
        {t(PRIORITY_KEY[p] ?? 'tasks.priority.none')}
      </span>
    );
  };

  const renderAssigneeCell = (task: TaskListItem): ReactElement => {
    const count = task.assigneeCount;
    if (count === 0) return <span className={css.mutedText}>—</span>;
    return <span>{t('tasks.assignee.count', { count })}</span>;
  };

  const renderDueCell = (task: TaskListItem, rowIdx: number): ReactElement => {
    const dueOn = task.dueOn;
    const overdue =
      isOverdue(dueOn, zone) && task.derivedState !== 'done' && task.derivedState !== 'cancelled';

    if (isEditing(rowIdx, 'due')) {
      return (
        <div className={css.cellDateInput}>
          <DatePicker
            value={editDraft}
            onChange={(next) => {
              setEditDraft(next);
              commitEdit(rowIdx, 'due', next);
              setEditingCell(null);
            }}
            weekdayLabels={weekdayLabels}
            weekStart={weekStart}
            formatMonthYear={formatMonthYear}
            prevLabel={t('calendar.prev')}
            nextLabel={t('calendar.next')}
            triggerLabel={editDraft ? formatDate(editDraft, locale) : t('common.date.placeholder')}
            {...(css.cellDateTrigger ? { className: css.cellDateTrigger } : {})}
            onClear={() => {
              setEditDraft('');
              commitEdit(rowIdx, 'due', '');
              setEditingCell(null);
            }}
            clearLabel={t('common.date.clear')}
          />
        </div>
      );
    }

    return (
      <span className={overdue ? css.overdueText : undefined}>
        {dueOn ? formatDate(dueOn, locale) : <span className={css.mutedText}>—</span>}
      </span>
    );
  };

  const renderUpdatedCell = (task: TaskListItem): ReactElement => {
    return <span className={css.mutedText}>{formatEpoch(task.updatedAt, locale) ?? '—'}</span>;
  };

  /* ── Row renderer ──────────────────────────────────────────── */

  const virtualItems = virtualizer.getVirtualItems();

  const handleWrapperKeyDown = (e: React.KeyboardEvent<HTMLDivElement>): void => {
    if (e.key !== 'Escape') return;
    if (editingCell) return;
    if (selectedRows.size === 0) return;
    e.preventDefault();
    clearSelection();
  };

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: layout wrapper that only intercepts grid-level keyboard shortcuts (Escape clears selection); the interactive grid lives in the child with role="grid".
    <div className={css.wrapper} onKeyDown={handleWrapperKeyDown}>
      {selectedIds.length > 0 && (
        <BulkActionBar selectedIds={selectedIds} onClear={clearSelection} />
      )}
      {mayBeTruncated ? (
        <p className={css.limitNotice}>{t('tasks.limit_notice', { count: TASKS_QUERY_LIMIT })}</p>
      ) : null}

      <div
        ref={parentRef}
        className={css.tableContainer}
        role="grid"
        aria-label={t('tasks.title')}
        aria-rowcount={tasks.length + 1}
      >
        {/* Header */}
        <div className={css.headerRow} role="row" aria-rowindex={1}>
          <div className={css.headerCellCheckbox} role="columnheader">
            <input
              type="checkbox"
              checked={selectedRows.size === tasks.length && tasks.length > 0}
              onChange={toggleAll}
              aria-label={t('tasks.bulk.selected', { count: tasks.length })}
            />
          </div>
          <div className={css.headerCell} role="columnheader">
            {t('tasks.columns.title')}
          </div>
          <div className={css.headerCell} role="columnheader">
            {t('tasks.columns.status')}
          </div>
          <div className={css.headerCell} role="columnheader">
            {t('tasks.columns.priority')}
          </div>
          <div className={`${css.headerCell} ${css.colAssignee}`} role="columnheader">
            {t('tasks.columns.assignee')}
          </div>
          <div className={`${css.headerCell} ${css.colDue}`} role="columnheader">
            {t('tasks.columns.due')}
          </div>
          <div className={`${css.headerCell} ${css.colUpdated}`} role="columnheader">
            {t('tasks.columns.updated')}
          </div>
        </div>

        {/* Virtualized body */}
        <div
          className={css.virtualContainer}
          style={{ blockSize: `${virtualizer.getTotalSize()}px` }}
        >
          <div
            className={css.virtualInner}
            style={{
              transform: `translateY(${virtualItems[0]?.start ?? 0}px)`,
            }}
          >
            {virtualItems.map((virtualRow) => {
              const task = tasks[virtualRow.index];
              if (!task) return null;
              const rowIdx = virtualRow.index;
              const isSelected = selectedRows.has(task.id);

              return (
                <div
                  key={task.id}
                  data-index={virtualRow.index}
                  ref={virtualizer.measureElement}
                  className={`${css.bodyRow ?? ''} ${isSelected ? (css.bodyRowSelected ?? '') : ''}`}
                  role="row"
                  aria-rowindex={rowIdx + 2}
                  aria-selected={isSelected}
                >
                  {/* Checkbox */}
                  <div className={css.cellCheckbox} role="gridcell">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleRow(task.id)}
                      aria-label={task.title}
                    />
                  </div>

                  {/* Title */}
                  <div
                    className={cellClassName(rowIdx, 'title', true)}
                    role="gridcell"
                    onClick={() => startEdit(rowIdx, 'title')}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        startEdit(rowIdx, 'title');
                      }
                    }}
                    tabIndex={isActive(rowIdx, 'title') ? 0 : -1}
                  >
                    {renderTitleCell(task, rowIdx)}
                  </div>

                  {/* Status */}
                  <div
                    className={cellClassName(rowIdx, 'status', true)}
                    role="gridcell"
                    onClick={() => startEdit(rowIdx, 'status')}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        startEdit(rowIdx, 'status');
                      }
                    }}
                    tabIndex={isActive(rowIdx, 'status') ? 0 : -1}
                  >
                    {renderStatusCell(task, rowIdx)}
                  </div>

                  {/* Priority */}
                  <div
                    className={cellClassName(rowIdx, 'priority', true)}
                    role="gridcell"
                    onClick={() => startEdit(rowIdx, 'priority')}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        startEdit(rowIdx, 'priority');
                      }
                    }}
                    tabIndex={isActive(rowIdx, 'priority') ? 0 : -1}
                  >
                    {renderPriorityCell(task, rowIdx)}
                  </div>

                  {/* Assignee */}
                  <div
                    className={`${cellClassName(rowIdx, 'assignee', false)} ${css.colAssignee}`}
                    role="gridcell"
                  >
                    {renderAssigneeCell(task)}
                  </div>

                  {/* Due */}
                  <div
                    className={`${cellClassName(rowIdx, 'due', true)} ${css.colDue}`}
                    role="gridcell"
                    onClick={() => startEdit(rowIdx, 'due')}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        startEdit(rowIdx, 'due');
                      }
                    }}
                    tabIndex={isActive(rowIdx, 'due') ? 0 : -1}
                  >
                    {renderDueCell(task, rowIdx)}
                  </div>

                  {/* Updated */}
                  <div className={`${css.cell} ${css.colUpdated}`} role="gridcell">
                    {renderUpdatedCell(task)}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        {/* Empty state */}
        {tasks.length === 0 && (
          <div
            style={{
              padding: 'var(--nf-space-8)',
              textAlign: 'center',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('tasks.empty')}
          </div>
        )}
      </div>
    </div>
  );
}
