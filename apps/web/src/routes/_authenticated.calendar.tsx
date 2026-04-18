/**
 * /calendar — monthly grid of workspace tasks with a due date.
 *
 * Fetches tasks from all user workspaces via `GET /tasks?workspaceId=...`.
 * Renders a 7-column grid for the current month with up to N tasks
 * per cell; overflow collapses to "+N more". Supports drag-and-drop
 * to reschedule tasks and clicking a date to open a quick-create form.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import {
  type DragEvent,
  type FormEvent,
  type ReactElement,
  useCallback,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { Project } from '../features/projects/api';
import type { TaskPriority } from '../features/tasks/api';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { sdk } from '../lib/sdk';

type CalendarTask = components['schemas']['TaskListItem'] & { workspaceName?: string };

const WEEKDAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;

const STATE_COLOR: Record<string, string> = {
  open: 'var(--color-info, #3498db)',
  waiting: 'var(--color-warning, #f39c12)',
  review: 'var(--color-accent, #9b59b6)',
  done: 'var(--color-success, #27ae60)',
  cancelled: 'var(--color-muted, #95a5a6)',
};

const PRIORITIES: readonly TaskPriority[] = [0, 1, 2, 3, 4];

const PRIORITY_KEY: Record<TaskPriority, string> = {
  0: 'tasks.priority.none',
  1: 'tasks.priority.low',
  2: 'tasks.priority.medium',
  3: 'tasks.priority.high',
  4: 'tasks.priority.urgent',
};

/** Local-time YYYY-MM-DD for the start of `d`. */
function dateKey(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

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

// ---------------------------------------------------------------------------
// Quick-create dialog (extracted for readability)
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
  const [eventOn, setEventOn] = useState('');

  // Sync defaults when the dialog opens for a different date.
  const prevDueOn = useRef(dueOn);
  if (dueOn !== prevDueOn.current) {
    prevDueOn.current = dueOn;
    setStartOn(dueOn);
    setEndOn(dueOn);
    setEventOn('');
  }

  const createMut = useMutation({
    mutationFn: async () => {
      const { data, error } = await sdk.POST('/tasks', {
        body: {
          projectId,
          title: title.trim(),
          ...(description.trim() ? { description: description.trim() } : {}),
          ...(startOn ? { startOn } : {}),
          ...(eventOn ? { eventOn } : {}),
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
      setEventOn('');
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
    setEventOn('');
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

        <FormField label={t('tasks.form.event')}>
          {(control) => (
            <Input
              {...control}
              type="date"
              value={eventOn}
              onChange={(e) => setEventOn(e.currentTarget.value)}
            />
          )}
        </FormField>

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
                {PRIORITIES.map((p) => (
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

  // Quick-create dialog state
  const [createDate, setCreateDate] = useState<string | null>(null);

  const { data: workspaces } = useWorkspacesQuery();

  // Fetch tasks from every workspace the user belongs to.
  const taskQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['calendar', 'tasks', w.id] as const,
      staleTime: 30_000,
      queryFn: async (): Promise<CalendarTask[]> => {
        const { data, error } = await sdk.GET('/tasks', {
          params: { query: { workspaceId: w.id, limit: 200, offset: 0 } },
        });
        if (error || !data) return [];
        return (data.tasks ?? []).map((task) => ({ ...task, workspaceName: w.name }));
      },
    })),
  });

  const tasks = useMemo<CalendarTask[]>(() => {
    const out: CalendarTask[] = [];
    for (const q of taskQueries) {
      if (q.data) out.push(...q.data);
    }
    return out;
  }, [taskQueries]);

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
      for (const w of workspaces) {
        void qc.invalidateQueries({ queryKey: ['calendar', 'tasks', w.id] });
      }
      toaster.show({ tone: 'success', message: t('calendar.reschedule_success') });
    },
    onError: () => {
      toaster.show({ tone: 'danger', message: t('calendar.reschedule_error') });
    },
  });

  const handleDragStart = useCallback((taskId: string, fromDate: string) => {
    dragDataRef.current = { taskId, fromDate };
  }, []);

  const handleDragOver = useCallback((e: DragEvent, cellKey: string) => {
    e.preventDefault();
    setDragOverKey(cellKey);
  }, []);

  const handleDragLeave = useCallback(() => {
    setDragOverKey(null);
  }, []);

  const handleDrop = useCallback(
    (e: DragEvent, cellKey: string) => {
      e.preventDefault();
      setDragOverKey(null);
      const data = dragDataRef.current;
      if (!data || data.fromDate === cellKey) return;
      rescheduleMut.mutate({ taskId: data.taskId, dueOn: cellKey });
      dragDataRef.current = null;
    },
    [rescheduleMut],
  );

  // Fetch projects for all workspaces (for quick-create picker).
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
    for (const w of workspaces) {
      void qc.invalidateQueries({ queryKey: ['calendar', 'tasks', w.id] });
    }
  }, [workspaces, qc]);

  const cells = useMemo(() => buildMonthGrid(cursor.year, cursor.month), [cursor]);

  /** dueOn → tasks for the current month grid. */
  const byDate = useMemo(() => {
    const map = new Map<string, CalendarTask[]>();
    for (const task of tasks) {
      if (!task.dueOn) continue;
      if (task.derivedState === 'cancelled') continue;
      const arr = map.get(task.dueOn);
      if (arr) {
        arr.push(task);
      } else {
        map.set(task.dueOn, [task]);
      }
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => b.priority - a.priority);
    }
    return map;
  }, [tasks]);

  /** eventOn → tasks whose event date falls on this cell (separate from due). */
  const eventByDate = useMemo(() => {
    const map = new Map<string, CalendarTask[]>();
    for (const task of tasks) {
      if (!task.eventOn) continue;
      if (task.derivedState === 'cancelled') continue;
      // Skip if eventOn === dueOn (already shown in the due list).
      if (task.eventOn === task.dueOn) continue;
      const arr = map.get(task.eventOn);
      if (arr) {
        arr.push(task);
      } else {
        map.set(task.eventOn, [task]);
      }
    }
    return map;
  }, [tasks]);

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
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('calendar.subtitle')}</p>
      </header>

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '0.75rem',
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
                color: 'var(--color-muted)',
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
            const dayEvents = eventByDate.get(cell.key) ?? [];
            const totalCount = dayTasks.length + dayEvents.length;
            const isToday = cell.key === todayKey;
            const isDragOver = dragOverKey === cell.key;
            return (
              <div
                key={cell.key}
                onDragOver={(e) => {
                  handleDragOver(e, cell.key);
                }}
                onDragLeave={handleDragLeave}
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
                    ? 'var(--color-primary-subtle, rgba(52, 152, 219, 0.12))'
                    : cell.inMonth
                      ? 'var(--color-surface, rgba(127,127,127,0.05))'
                      : 'transparent',
                  border: isDragOver
                    ? '2px dashed var(--color-primary, #3498db)'
                    : isToday
                      ? '1px solid var(--color-accent, #9b59b6)'
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
                    color: isToday ? 'var(--color-accent, #9b59b6)' : 'var(--color-muted)',
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
                          background: 'var(--color-bg, rgba(255,255,255,0.04))',
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
                            background: STATE_COLOR[task.derivedState] ?? 'var(--color-muted)',
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
                        color: 'var(--color-muted)',
                        paddingInline: '0.25rem',
                      }}
                    >
                      {t('calendar.more', { count: dayTasks.length - 3 })}
                    </li>
                  ) : null}
                  {dayEvents.slice(0, 2).map((task) => (
                    <li key={`ev-${task.id}`}>
                      <Link
                        to="/tasks/$taskId"
                        params={{ taskId: task.id }}
                        title={`${task.title} · ${t('tasks.form.event')}`}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '0.25rem',
                          fontSize: '0.75rem',
                          color: 'inherit',
                          textDecoration: 'none',
                          padding: '0.125rem 0.25rem',
                          borderRadius: '0.25rem',
                          background: 'var(--nf-color-accent-subtle, rgba(155,89,182,0.08))',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
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
                            transform: 'rotate(45deg)',
                            background: 'var(--color-accent, #9b59b6)',
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
