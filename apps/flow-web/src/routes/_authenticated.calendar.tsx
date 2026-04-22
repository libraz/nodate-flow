/**
 * /calendar — unified cross-workspace calendar.
 *
 * Consumes two aggregated endpoints instead of per-workspace fan-out:
 *   - `GET /me/tasks-with-dates?from=&to=` (flow-api)   — tasks with due_on
 *   - `GET /me/calendar-events?start=&end=` (time-api)  — calendar events
 *
 * The month grid overlays three toggleable layers: task-due,
 * calendar events, and blocks (`kind='block'`). Dragging a task cell
 * reschedules it through itemkit (PATCH /tasks), and clicking a cell
 * opens the quick-create task dialog.
 */

import type { components } from '@nodate-flow/sdk';
import type { components as timeComponents } from '@nodate-flow/time-sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import {
  type DragEvent,
  type FormEvent,
  type ReactElement,
  useCallback,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { Project } from '../features/projects/api';
import { TASK_PRIORITIES, type TaskDerivedState, type TaskPriority } from '../features/tasks/api';
import { PRIORITY_KEY, STATE_COLOR } from '../features/tasks/constants';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { dateKey } from '../lib/date-utils';
import { sdk, timeSdk } from '../lib/sdk';

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

// ---------------------------------------------------------------------------
// Quick-create dialog
// ---------------------------------------------------------------------------

interface QuickCreateDialogProps {
  open: boolean;
  dateLabel: string;
  dueOn: string;
  projects: Project[];
  onClose: () => void;
  onCreated: () => void;
}

function QuickCreateDialog({
  open,
  dateLabel,
  dueOn,
  projects,
  onClose,
  onCreated,
}: QuickCreateDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [priority, setPriority] = useState<TaskPriority>(2);
  const [projectId, setProjectId] = useState<string>(projects[0]?.id ?? '');
  const [startOn, setStartOn] = useState(dueOn);
  const [endOn, setEndOn] = useState(dueOn);

  const prevDueOn = useRef(dueOn);
  if (dueOn !== prevDueOn.current) {
    prevDueOn.current = dueOn;
    setStartOn(dueOn);
    setEndOn(dueOn);
  }

  const createMut = useMutation({
    mutationFn: async () => {
      const { data, error } = await sdk.POST('/tasks', {
        body: {
          projectId,
          title: title.trim(),
          ...(description.trim() ? { description: description.trim() } : {}),
          ...(startOn ? { startOn } : {}),
          dueOn: endOn || dueOn,
          priority,
          visibility: 'project',
        },
      });
      if (error || !data) throw new Error('Failed to create');
      return data;
    },
    onSuccess: () => {
      toaster.show({ tone: 'success', message: t('tasks.created') });
      setTitle('');
      setDescription('');
      setPriority(2);
      setStartOn(dueOn);
      setEndOn(dueOn);
      onCreated();
    },
    onError: () => {
      toaster.show({ tone: 'danger', message: t('tasks.create_error') });
    },
  });

  const handleSubmit = (ev: FormEvent<HTMLFormElement>): void => {
    ev.preventDefault();
    if (!title.trim() || !projectId) return;
    createMut.mutate();
  };

  const handleClose = (): void => {
    if (createMut.isPending) return;
    setTitle('');
    setDescription('');
    setPriority(2);
    setStartOn(dueOn);
    setEndOn(dueOn);
    onClose();
  };

  return (
    <Dialog open={open} onClose={handleClose} title={`${t('tasks.new')} — ${dateLabel}`}>
      <form
        onSubmit={handleSubmit}
        style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', minInlineSize: '20rem' }}
      >
        <FormField label={t('tasks.form.title')}>
          {(control) => (
            <Input
              {...control}
              value={title}
              onChange={(e) => setTitle(e.currentTarget.value)}
              placeholder={t('tasks.title_placeholder')}
              autoFocus
            />
          )}
        </FormField>

        <FormField label={t('tasks.form.description')}>
          {(control) => (
            <Textarea
              {...control}
              value={description}
              onChange={(e) => setDescription(e.currentTarget.value)}
              rows={2}
            />
          )}
        </FormField>

        <div style={{ display: 'flex', gap: '0.5rem' }}>
          <FormField label={t('tasks.form.start')} style={{ flex: 1 }}>
            {(control) => (
              <Input
                {...control}
                type="date"
                value={startOn}
                onChange={(e) => setStartOn(e.currentTarget.value)}
              />
            )}
          </FormField>
          <FormField label={t('tasks.form.due')} style={{ flex: 1 }}>
            {(control) => (
              <Input
                {...control}
                type="date"
                value={endOn}
                onChange={(e) => setEndOn(e.currentTarget.value)}
              />
            )}
          </FormField>
        </div>

        <div style={{ display: 'flex', gap: '0.5rem' }}>
          {projects.length > 1 ? (
            <FormField label={t('tasks.select_project')} style={{ flex: 1 }}>
              {(control) => (
                <Select
                  {...control}
                  value={projectId}
                  onChange={(e) => setProjectId(e.currentTarget.value)}
                >
                  {projects.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              )}
            </FormField>
          ) : null}

          <FormField label={t('tasks.form.priority')} style={{ flex: 1 }}>
            {(control) => (
              <Select
                {...control}
                value={String(priority)}
                onChange={(e) => setPriority(Number(e.currentTarget.value) as TaskPriority)}
              >
                {TASK_PRIORITIES.map((p) => (
                  <option key={p} value={String(p)}>
                    {t(PRIORITY_KEY[p])}
                  </option>
                ))}
              </Select>
            )}
          </FormField>
        </div>

        <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
          <Button type="button" variant="ghost" onClick={handleClose}>
            {t('tasks.form.cancel')}
          </Button>
          <Button type="submit" disabled={createMut.isPending || !title.trim() || !projectId}>
            {createMut.isPending ? t('common.loading') : t('tasks.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Main calendar route
// ---------------------------------------------------------------------------

interface LayerFlags {
  tasksDue: boolean;
  events: boolean;
  blocks: boolean;
}

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

  const [createDate, setCreateDate] = useState<string | null>(null);
  const [layers, setLayers] = useState<LayerFlags>({ tasksDue: true, events: true, blocks: false });
  const tasksDueCheckboxId = useId();
  const eventsCheckboxId = useId();
  const blocksCheckboxId = useId();

  const { data: workspaces } = useWorkspacesQuery();

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

  const rescheduleMut = useMutation({
    mutationFn: async ({ taskId, dueOn }: { taskId: string; dueOn: string }) => {
      const { data, error } = await sdk.PATCH('/tasks/{id}', {
        params: { path: { id: taskId } },
        body: { dueOn },
      });
      if (error || !data) throw new Error('Failed to reschedule');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
      toaster.show({ tone: 'success', message: t('calendar.reschedule_success') });
    },
    onError: () => {
      toaster.show({ tone: 'danger', message: t('calendar.reschedule_error') });
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

  const handleCellClick = useCallback((cellKey: string) => {
    setCreateDate(cellKey);
  }, []);

  const handleCreated = useCallback(() => {
    setCreateDate(null);
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
      const isBlock = ev.kind === 'block';
      if (isBlock && !layers.blocks) continue;
      if (!isBlock && !layers.events) continue;
      const k = dateKeyFromUnix(ev.startAt);
      const arr = map.get(k);
      if (arr) arr.push(ev);
      else map.set(k, [ev]);
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => (a.startAt ?? 0) - (b.startAt ?? 0));
    }
    return map;
  }, [events, layers.events, layers.blocks]);

  const todayKey = dateKey(today);
  const monthLabel = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }).format(
        new Date(cursor.year, cursor.month, 1),
      ),
    [locale, cursor],
  );

  const createDateLabel = useMemo(() => {
    if (!createDate) return '';
    const d = new Date(`${createDate}T00:00:00`);
    return new Intl.DateTimeFormat(locale, {
      month: 'long',
      day: 'numeric',
      weekday: 'short',
    }).format(d);
  }, [createDate, locale]);

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

        <fieldset
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '0.75rem',
            border: 'none',
            margin: 0,
            padding: 0,
            fontSize: '0.8125rem',
          }}
        >
          <legend style={{ position: 'absolute', inlineSize: 1, blockSize: 1, overflow: 'hidden' }}>
            {t('calendar.layers')}
          </legend>
          <label
            htmlFor={tasksDueCheckboxId}
            style={{ display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
          >
            <Checkbox
              id={tasksDueCheckboxId}
              checked={layers.tasksDue}
              onChange={(e) => setLayers((s) => ({ ...s, tasksDue: e.currentTarget.checked }))}
            />
            {t('calendar.layer.tasks_due')}
          </label>
          <label
            htmlFor={eventsCheckboxId}
            style={{ display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
          >
            <Checkbox
              id={eventsCheckboxId}
              checked={layers.events}
              onChange={(e) => setLayers((s) => ({ ...s, events: e.currentTarget.checked }))}
            />
            {t('calendar.layer.events')}
          </label>
          <label
            htmlFor={blocksCheckboxId}
            style={{ display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
          >
            <Checkbox
              id={blocksCheckboxId}
              checked={layers.blocks}
              onChange={(e) => setLayers((s) => ({ ...s, blocks: e.currentTarget.checked }))}
            />
            {t('calendar.layer.blocks')}
          </label>
        </fieldset>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column' }}>
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
                  if (cell.inMonth) handleCellClick(cell.key);
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    if (cell.inMonth) handleCellClick(cell.key);
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
                    const isBlock = ev.kind === 'block';
                    return (
                      <li key={`ev-${ev.id}`}>
                        <span
                          title={`${ev.title} · ${ev.workspaceName}`}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: '0.25rem',
                            fontSize: '0.75rem',
                            padding: '0.125rem 0.25rem',
                            borderRadius: '0.25rem',
                            background: isBlock
                              ? 'var(--nf-color-bg-subtle)'
                              : 'var(--nf-color-accent-subtle)',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}
                        >
                          <span
                            aria-hidden
                            style={{
                              inlineSize: '0.5rem',
                              blockSize: '0.5rem',
                              transform: 'rotate(45deg)',
                              background: isBlock
                                ? 'var(--nf-color-fg-muted)'
                                : 'var(--nf-color-accent)',
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
                        </span>
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

      <QuickCreateDialog
        open={createDate !== null}
        dateLabel={createDateLabel}
        dueOn={createDate ?? ''}
        projects={allProjects}
        onClose={() => setCreateDate(null)}
        onCreated={handleCreated}
      />
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/calendar')({
  component: CalendarRoute,
});
