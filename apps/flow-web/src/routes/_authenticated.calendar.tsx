/**
 * /calendar — unified cross-workspace calendar.
 *
 * Consumes two aggregated endpoints instead of per-workspace fan-out:
 *   - `GET /me/tasks-with-dates?from=&to=` (flow-api)   — tasks with due_on
 *   - `GET /me/calendar-events?start=&end=` (time-api)  — calendar events
 *
 * The month grid overlays five toggleable layers: task-due, calendar
 * events, blocks, free, and milestones. Dragging a task cell
 * reschedules it through itemkit (PATCH /tasks). Clicking a cell
 * opens the unified {@link EventDialog} in create mode (default kind:
 * event; shift-click shortcuts to block). Clicking an event pill opens
 * the same dialog in edit mode for that row; clicking a task pill
 * navigates to the task detail route (editing a task is out of scope
 * for the calendar dialog).
 */

import type { components } from '@nodate-flow/sdk';
import type { components as timeComponents } from '@nodate-flow/time-sdk';
import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { type DragEvent, type ReactElement, useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import EventDialog, {
  type EventDialogMode,
  type ItemKind,
} from '../features/calendar-events/event-dialog';
import PendingInvitesPanel from '../features/calendar-invites/pending-invites-panel';
import calendarLayoutStyles from '../features/calendar-invites/pending-invites-panel.module.css';
import type { Project } from '../features/projects/api';
import type { TaskDerivedState } from '../features/tasks/api';
import { STATE_COLOR } from '../features/tasks/constants';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { type ApiError, toApiError } from '../lib/api-error';
import { dateKey } from '../lib/date-utils';
import { sdk, timeSdk } from '../lib/sdk';
import { useActiveWorkspaceId } from '../lib/use-current-workspace';

/**
 * Error code emitted by the backend when a PATCH /tasks request would
 * leave `dueOn < startedOn`. Matched against `ApiError.code` to surface
 * a targeted toast instead of a generic failure message.
 */
const DUE_BEFORE_START_CODE = 'VALIDATION.BODY.DUE_BEFORE_START';

type CalendarTask = components['schemas']['MyTaskListItem'];
type CalendarEvent = timeComponents['schemas']['MyCalendarEventResponse'];

const WEEKDAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;

/** Number of days in a given (year, monthIndex). */
function daysInMonth(year: number, monthIndex: number): number {
  return new Date(year, monthIndex + 1, 0).getDate();
}

/** 0..6 with Monday = 0. */
function mondayBasedDow(d: Date): number {
  return (d.getDay() + 6) % 7;
}

interface MonthCell {
  date: Date;
  key: string;
  inMonth: boolean;
}

function buildMonthGrid(year: number, monthIndex: number): MonthCell[] {
  const first = new Date(year, monthIndex, 1);
  const lead = mondayBasedDow(first);
  const cells: MonthCell[] = [];
  for (let i = lead; i > 0; i--) {
    const d = new Date(year, monthIndex, 1 - i);
    cells.push({ date: d, key: dateKey(d), inMonth: false });
  }
  const total = daysInMonth(year, monthIndex);
  for (let day = 1; day <= total; day++) {
    const d = new Date(year, monthIndex, day);
    cells.push({ date: d, key: dateKey(d), inMonth: true });
  }
  while (cells.length % 7 !== 0) {
    const last = cells[cells.length - 1];
    if (!last) break;
    const d = new Date(last.date);
    d.setDate(d.getDate() + 1);
    cells.push({ date: d, key: dateKey(d), inMonth: false });
  }
  return cells;
}

/** Unix seconds → YYYY-MM-DD in the local tz. */
function dateKeyFromUnix(unixSeconds: number): string {
  return dateKey(new Date(unixSeconds * 1000));
}

/**
 * Differentiate calendar-event pills by kind. Returns inline style
 * fragments merged into the pill button, plus the 45-degree marker
 * colour rendered inside it.
 *
 * - event: flat subtle fill.
 * - block: subtle fill + diagonal stripe (via repeating gradient).
 * - free: subtle fill + dashed border.
 * - milestone: transparent background + bottom border only.
 */
function pillStyleForKind(kind: string): {
  background?: string;
  border?: string;
  borderBlockEnd?: string;
  backgroundImage?: string;
  markerColor: string;
} {
  switch (kind) {
    case 'block':
      return {
        background: 'var(--nf-cal-block-subtle)',
        backgroundImage:
          'repeating-linear-gradient(135deg, transparent 0 6px, rgba(0,0,0,0.04) 6px 8px)',
        markerColor: 'var(--nf-cal-block-color)',
      };
    case 'free':
      return {
        background: 'var(--nf-cal-free-subtle)',
        border: '1px dashed var(--nf-cal-free-color)',
        markerColor: 'var(--nf-cal-free-color)',
      };
    case 'milestone':
      return {
        background: 'transparent',
        borderBlockEnd: '2px solid var(--nf-cal-milestone-color)',
        markerColor: 'var(--nf-cal-milestone-color)',
      };
    default:
      return {
        background: 'var(--nf-cal-event-subtle)',
        markerColor: 'var(--nf-cal-event-color)',
      };
  }
}

// ---------------------------------------------------------------------------
// Main calendar route
// ---------------------------------------------------------------------------

interface LayerFlags {
  tasksDue: boolean;
  events: boolean;
  blocks: boolean;
  free: boolean;
  milestone: boolean;
}

/**
 * Open-state of the unified event dialog. Create mode carries the
 * clicked cell's date + the kind the picker should land on; edit mode
 * carries the full event row so the dialog can hydrate without a
 * second fetch.
 */
type EditTarget =
  | { mode: 'create'; date: string; initialItemKind: ItemKind }
  | { mode: 'edit'; event: CalendarEvent };

function CalendarRoute(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const qc = useQueryClient();
  const today = new Date();
  const [cursor, setCursor] = useState<{ year: number; month: number }>({
    year: today.getFullYear(),
    month: today.getMonth(),
  });
  const [dragOverKey, setDragOverKey] = useState<string | null>(null);
  const dragDataRef = useRef<{ taskId: string; fromDate: string } | null>(null);
  const enterCountRef = useRef<Record<string, number>>({});

  const [editTarget, setEditTarget] = useState<EditTarget | null>(null);
  const [layers, setLayers] = useState<LayerFlags>({
    tasksDue: true,
    events: true,
    blocks: false,
    free: false,
    milestone: true,
  });

  const { data: workspaces } = useWorkspacesQuery();
  const activeWsId = useActiveWorkspaceId();

  // Range that covers the full 42-cell month grid (may span adjacent months).
  const { fromDate, toDate, fromIso, toIso } = useMemo(() => {
    const cells = buildMonthGrid(cursor.year, cursor.month);
    const first = cells[0]?.date ?? new Date(cursor.year, cursor.month, 1);
    const last = cells[cells.length - 1]?.date ?? new Date(cursor.year, cursor.month, 1);
    const start = new Date(first);
    start.setHours(0, 0, 0, 0);
    const end = new Date(last);
    end.setHours(23, 59, 59, 999);
    return {
      fromDate: dateKey(start),
      toDate: dateKey(last),
      fromIso: start.toISOString(),
      toIso: end.toISOString(),
    };
  }, [cursor]);

  // Single cross-workspace task query (flow-api /me/tasks-with-dates).
  const tasksQuery = useQuery({
    queryKey: ['calendar', 'me-tasks', fromDate, toDate] as const,
    staleTime: 30_000,
    queryFn: async (): Promise<CalendarTask[]> => {
      const { data, error } = await sdk.GET('/me/tasks-with-dates', {
        params: { query: { from: fromDate, to: toDate, limit: 1000 } },
      });
      if (error || !data) return [];
      return data.tasks ?? [];
    },
  });

  const tasks = tasksQuery.data ?? [];

  // Single cross-workspace event query (time-api /me/calendar-events).
  const eventsQuery = useQuery({
    queryKey: ['calendar', 'me-events', fromIso, toIso] as const,
    staleTime: 30_000,
    queryFn: async (): Promise<CalendarEvent[]> => {
      const { data, error } = await timeSdk.GET('/me/calendar-events', {
        params: { query: { start: fromIso, end: toIso } },
      });
      if (error || !data) return [];
      return data.events ?? [];
    },
  });

  const events = eventsQuery.data ?? [];

  const rescheduleMut = useMutation<
    components['schemas']['Task'],
    ApiError,
    { taskId: string; dueOn: string }
  >({
    mutationFn: async ({ taskId, dueOn }) => {
      const { data, error } = await sdk.PATCH('/tasks/{id}', {
        params: { path: { id: taskId } },
        body: { dueOn },
      });
      if (error || !data) throw toApiError(error, 'Failed to reschedule');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
      toaster.show({ tone: 'success', message: t('calendar.reschedule_success') });
    },
    onError: (err) => {
      // Pessimistic update: the calendar pill only moves once the
      // mutation succeeds (no optimistic `onMutate`). The subsequent
      // refetch on settle brings the original data back, so no manual
      // rollback is needed — a toast is sufficient.
      if (err.code === DUE_BEFORE_START_CODE) {
        toaster.show({
          tone: 'danger',
          message: t(`errors:${DUE_BEFORE_START_CODE}`, { keySeparator: false }),
        });
        return;
      }
      toaster.show({ tone: 'danger', message: t('calendar.reschedule_error') });
      // Refetch to make sure the pill reflects server state in case any
      // optimistic UI ever gets added to this path.
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
    },
  });

  const handleDragStart = useCallback((taskId: string, fromDate: string) => {
    dragDataRef.current = { taskId, fromDate };
  }, []);

  const handleDragEnter = useCallback((e: DragEvent, cellKey: string) => {
    e.preventDefault();
    const count = (enterCountRef.current[cellKey] ?? 0) + 1;
    enterCountRef.current[cellKey] = count;
    if (count === 1) setDragOverKey(cellKey);
  }, []);

  const handleDragOver = useCallback((e: DragEvent) => {
    e.preventDefault();
  }, []);

  const handleDragLeave = useCallback((cellKey: string) => {
    const count = Math.max(0, (enterCountRef.current[cellKey] ?? 0) - 1);
    enterCountRef.current[cellKey] = count;
    if (count === 0) setDragOverKey((prev) => (prev === cellKey ? null : prev));
  }, []);

  const handleDrop = useCallback(
    (e: DragEvent, cellKey: string) => {
      e.preventDefault();
      setDragOverKey(null);
      enterCountRef.current = {};
      const data = dragDataRef.current;
      if (!data || data.fromDate === cellKey) return;
      rescheduleMut.mutate({ taskId: data.taskId, dueOn: cellKey });
      dragDataRef.current = null;
    },
    [rescheduleMut],
  );

  // Projects per workspace, just for the quick-create project picker.
  const projectQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['calendar', 'projects', w.id] as const,
      staleTime: 60_000,
      queryFn: async (): Promise<Project[]> => {
        const { data, error } = await sdk.GET('/workspaces/{wsId}/projects', {
          params: { path: { wsId: w.id } },
        });
        if (error || !data) return [];
        return data.projects ?? [];
      },
    })),
  });

  const allProjects = useMemo<Project[]>(() => {
    const out: Project[] = [];
    for (const q of projectQueries) {
      if (q.data) out.push(...q.data);
    }
    return out;
  }, [projectQueries]);

  const handleCellClick = useCallback((cellKey: string, shiftKey: boolean) => {
    // Shift+click on a cell is a power-user quick path to the Block kind;
    // the dialog segmented control still lets the user switch.
    const initialItemKind: ItemKind = shiftKey ? 'block' : 'event';
    setEditTarget({ mode: 'create', date: cellKey, initialItemKind });
  }, []);

  const handleSaved = useCallback(() => {
    setEditTarget(null);
    void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
    void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
  }, [qc]);

  const cells = useMemo(() => buildMonthGrid(cursor.year, cursor.month), [cursor]);

  /** dueOn → tasks for the current grid (after layer filtering). */
  const byDate = useMemo(() => {
    const map = new Map<string, CalendarTask[]>();
    if (!layers.tasksDue) return map;
    for (const task of tasks) {
      if (!task.dueOn) continue;
      if (task.derivedState === 'cancelled') continue;
      const arr = map.get(task.dueOn);
      if (arr) arr.push(task);
      else map.set(task.dueOn, [task]);
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => b.priority - a.priority);
    }
    return map;
  }, [tasks, layers.tasksDue]);

  /** dateKey → calendar events (after layer filtering by kind). */
  const eventsByDate = useMemo(() => {
    const map = new Map<string, CalendarEvent[]>();
    for (const ev of events) {
      if (!ev.startAt) continue;
      // Each kind gates on its own layer flag; unknown kinds fall through
      // to the generic `events` flag so the UI never silently hides them.
      const k = ev.kind;
      if (k === 'block' && !layers.blocks) continue;
      if (k === 'free' && !layers.free) continue;
      if (k === 'milestone' && !layers.milestone) continue;
      if (k !== 'block' && k !== 'free' && k !== 'milestone' && !layers.events) continue;
      const key = dateKeyFromUnix(ev.startAt);
      const arr = map.get(key);
      if (arr) arr.push(ev);
      else map.set(key, [ev]);
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => (a.startAt ?? 0) - (b.startAt ?? 0));
    }
    return map;
  }, [events, layers.events, layers.blocks, layers.free, layers.milestone]);

  const todayKey = dateKey(today);
  const monthLabel = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }).format(
        new Date(cursor.year, cursor.month, 1),
      ),
    [locale, cursor],
  );

  const goPrev = (): void => {
    setCursor((c) => {
      const m = c.month - 1;
      return m < 0 ? { year: c.year - 1, month: 11 } : { year: c.year, month: m };
    });
  };
  const goNext = (): void => {
    setCursor((c) => {
      const m = c.month + 1;
      return m > 11 ? { year: c.year + 1, month: 0 } : { year: c.year, month: m };
    });
  };
  const goToday = (): void => {
    setCursor({ year: today.getFullYear(), month: today.getMonth() });
  };

  return (
    <section
      style={{
        padding: 'clamp(1.5rem, 4vw, 2.5rem)',
        display: 'flex',
        flexDirection: 'column',
        gap: '1.5rem',
        maxInlineSize: '78rem',
        marginInline: 'auto',
        inlineSize: '100%',
      }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <h1
          style={{
            margin: 0,
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1.75rem, 3vw, 2.25rem)',
          }}
        >
          {t('calendar.title')}
        </h1>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('calendar.subtitle')}</p>
      </header>

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '0.75rem',
          flexWrap: 'wrap',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('calendar.prev')}
            onClick={goPrev}
          >
            <ChevronLeft size={16} aria-hidden />
          </Button>
          <h2 style={{ margin: 0, fontSize: '1.125rem', minInlineSize: '10rem' }}>{monthLabel}</h2>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('calendar.next')}
            onClick={goNext}
          >
            <ChevronRight size={16} aria-hidden />
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={goToday}>
            {t('calendar.today')}
          </Button>
        </div>

        <ToggleChipGroup label={t('calendar.layers')}>
          <ToggleChip
            pressed={layers.tasksDue}
            onPressedChange={(v) => setLayers((s) => ({ ...s, tasksDue: v }))}
            color="var(--nf-cal-task-color)"
          >
            {t('calendar.layer.tasks_due')}
          </ToggleChip>
          <ToggleChip
            pressed={layers.events}
            onPressedChange={(v) => setLayers((s) => ({ ...s, events: v }))}
            color="var(--nf-cal-event-color)"
          >
            {t('calendar.layer.events')}
          </ToggleChip>
          <ToggleChip
            pressed={layers.blocks}
            onPressedChange={(v) => setLayers((s) => ({ ...s, blocks: v }))}
            color="var(--nf-cal-block-color)"
          >
            {t('calendar.layer.blocks')}
          </ToggleChip>
          <ToggleChip
            pressed={layers.free}
            onPressedChange={(v) => setLayers((s) => ({ ...s, free: v }))}
            color="var(--nf-cal-free-color)"
          >
            {t('calendar.layer.free')}
          </ToggleChip>
          <ToggleChip
            pressed={layers.milestone}
            onPressedChange={(v) => setLayers((s) => ({ ...s, milestone: v }))}
            color="var(--nf-cal-milestone-color)"
          >
            {t('calendar.layer.milestone')}
          </ToggleChip>
        </ToggleChipGroup>
      </div>

      <div className={calendarLayoutStyles.layout}>
        <div style={{ display: 'flex', flexDirection: 'column', minInlineSize: 0 }}>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(7, minmax(0, 1fr))',
              gap: '0.25rem',
              marginBlockEnd: '0.25rem',
            }}
          >
            {WEEKDAY_KEYS.map((wk) => (
              <div
                key={wk}
                style={{
                  fontSize: '0.75rem',
                  textTransform: 'uppercase',
                  letterSpacing: '0.05em',
                  color: 'var(--nf-color-fg-muted)',
                  paddingInline: '0.5rem',
                }}
              >
                {t(`calendar.weekday.${wk}`)}
              </div>
            ))}
          </div>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(7, minmax(0, 1fr))',
              gap: '0.25rem',
            }}
          >
            {cells.map((cell) => {
              const dayTasks = byDate.get(cell.key) ?? [];
              const dayEvents = eventsByDate.get(cell.key) ?? [];
              const totalCount = dayTasks.length + dayEvents.length;
              const isToday = cell.key === todayKey;
              const isDragOver = dragOverKey === cell.key;
              return (
                <div
                  key={cell.key}
                  onDragEnter={(e) => {
                    handleDragEnter(e, cell.key);
                  }}
                  onDragOver={handleDragOver}
                  onDragLeave={() => {
                    handleDragLeave(cell.key);
                  }}
                  onDrop={(e) => {
                    handleDrop(e, cell.key);
                  }}
                  onClick={(e) => {
                    if ((e.target as HTMLElement).closest('a, button')) return;
                    if (cell.inMonth) handleCellClick(cell.key, e.shiftKey);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      if (cell.inMonth) handleCellClick(cell.key, e.shiftKey);
                    }
                  }}
                  role={cell.inMonth ? 'button' : undefined}
                  tabIndex={cell.inMonth ? 0 : undefined}
                  style={{
                    minBlockSize: '7rem',
                    padding: '0.5rem',
                    borderRadius: '0.5rem',
                    background: isDragOver
                      ? 'var(--nf-color-accent-subtle)'
                      : cell.inMonth
                        ? 'var(--nf-color-surface)'
                        : 'transparent',
                    border: isDragOver
                      ? '2px dashed var(--nf-color-accent)'
                      : isToday
                        ? '1px solid var(--nf-color-accent)'
                        : '1px solid transparent',
                    opacity: cell.inMonth ? 1 : 0.4,
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '0.25rem',
                    overflow: 'hidden',
                    cursor: cell.inMonth ? 'pointer' : 'default',
                    transition: 'background 0.15s, border 0.15s',
                  }}
                  title={cell.inMonth ? t('calendar.click_to_add') : undefined}
                >
                  <div
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      fontSize: '0.75rem',
                      fontVariantNumeric: 'tabular-nums',
                      color: isToday ? 'var(--nf-color-accent)' : 'var(--nf-color-fg-muted)',
                      fontWeight: isToday ? 600 : 400,
                    }}
                  >
                    <span>{cell.date.getDate()}</span>
                    {totalCount > 0 ? <span>{totalCount}</span> : null}
                  </div>
                  <ul
                    style={{
                      listStyle: 'none',
                      margin: 0,
                      padding: 0,
                      display: 'flex',
                      flexDirection: 'column',
                      gap: '0.125rem',
                      overflow: 'hidden',
                    }}
                  >
                    {dayTasks.slice(0, 3).map((task) => (
                      <li key={task.id}>
                        <Link
                          to="/tasks/$taskId"
                          params={{ taskId: task.id }}
                          title={`${task.title} · ${task.workspaceName}`}
                          draggable
                          onDragStart={(e) => {
                            e.dataTransfer.effectAllowed = 'move';
                            handleDragStart(task.id, cell.key);
                          }}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '0.25rem',
                            fontSize: '0.75rem',
                            color: 'inherit',
                            textDecoration: 'none',
                            padding: '0.125rem 0.25rem',
                            borderRadius: '0.25rem',
                            background: 'var(--nf-color-bg)',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                            cursor: 'grab',
                          }}
                          onClick={(e) => {
                            e.stopPropagation();
                          }}
                        >
                          <span
                            aria-hidden
                            style={{
                              inlineSize: '0.5rem',
                              blockSize: '0.5rem',
                              borderRadius: '999px',
                              background:
                                STATE_COLOR[task.derivedState as TaskDerivedState] ??
                                'var(--nf-color-fg-muted)',
                              flexShrink: 0,
                            }}
                          />
                          <span
                            style={{
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                            }}
                          >
                            {task.title}
                          </span>
                        </Link>
                      </li>
                    ))}
                    {dayTasks.length > 3 ? (
                      <li
                        style={{
                          fontSize: '0.6875rem',
                          color: 'var(--nf-color-fg-muted)',
                          paddingInline: '0.25rem',
                        }}
                      >
                        {t('calendar.more', { count: dayTasks.length - 3 })}
                      </li>
                    ) : null}
                    {dayEvents.slice(0, 2).map((ev) => {
                      const pill = pillStyleForKind(ev.kind);
                      return (
                        <li key={`ev-${ev.id}`}>
                          <button
                            type="button"
                            title={`${ev.title} · ${ev.workspaceName}`}
                            aria-label={t('calendar.event_detail.open_label', {
                              title: ev.title,
                              workspace: ev.workspaceName,
                            })}
                            onClick={(e) => {
                              e.stopPropagation();
                              setEditTarget({ mode: 'edit', event: ev });
                            }}
                            style={{
                              all: 'unset',
                              boxSizing: 'border-box',
                              inlineSize: '100%',
                              display: 'flex',
                              alignItems: 'center',
                              gap: '0.25rem',
                              fontSize: '0.75rem',
                              padding: '0.125rem 0.25rem',
                              borderRadius: '0.25rem',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                              cursor: 'pointer',
                              ...pill,
                            }}
                          >
                            <span
                              aria-hidden
                              style={{
                                inlineSize: '0.5rem',
                                blockSize: '0.5rem',
                                transform: 'rotate(45deg)',
                                background: pill.markerColor,
                                flexShrink: 0,
                              }}
                            />
                            <span
                              style={{
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                whiteSpace: 'nowrap',
                              }}
                            >
                              {ev.title}
                            </span>
                          </button>
                        </li>
                      );
                    })}
                    {dayEvents.length > 2 ? (
                      <li
                        style={{
                          fontSize: '0.6875rem',
                          color: 'var(--nf-color-fg-muted)',
                          paddingInline: '0.25rem',
                        }}
                      >
                        {t('calendar.more', { count: dayEvents.length - 2 })}
                      </li>
                    ) : null}
                  </ul>
                </div>
              );
            })}
          </div>
        </div>

        <PendingInvitesPanel />
      </div>

      {editTarget !== null ? (
        <EventDialog
          open
          workspaceId={
            editTarget.mode === 'edit' ? editTarget.event.workspaceId : (activeWsId ?? '')
          }
          projects={allProjects}
          mode={toDialogMode(editTarget)}
          onClose={() => setEditTarget(null)}
          onSaved={handleSaved}
        />
      ) : null}
    </section>
  );
}

/** Convert the route-local EditTarget shape to EventDialog's mode prop. */
function toDialogMode(target: EditTarget): EventDialogMode {
  if (target.mode === 'create') {
    return {
      kind: 'create',
      date: target.date,
      initialItemKind: target.initialItemKind,
    };
  }
  const ev = target.event;
  // Task kind is out of scope for edit-mode; the `/calendar` route
  // pills only open this dialog for calendar event rows. Unknown
  // kinds fall through to 'event' so the UI never crashes.
  const kind =
    ev.kind === 'block' || ev.kind === 'free' || ev.kind === 'milestone' ? ev.kind : 'event';
  return {
    kind: 'edit',
    eventId: ev.id,
    calendarId: ev.calendarId,
    initialKind: kind,
    event: ev,
  };
}

export const Route = createFileRoute('/_authenticated/calendar')({
  component: CalendarRoute,
});
