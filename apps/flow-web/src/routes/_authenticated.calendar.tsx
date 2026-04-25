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
 *
 * The grid honours the user's `me.weekStart` preference — Monday,
 * Sunday, or Saturday — for both the header order and the leading
 * blank cells. Weekend tints follow the actual weekday key at each
 * column index, so a Sunday-first or Saturday-first grid still paints
 * Sun-red / Sat-blue in the right place.
 */

import type { components } from '@nodate-flow/sdk';
import type { components as timeComponents } from '@nodate-flow/time-sdk';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, createFileRoute } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight, Users } from 'lucide-react';
import { type DragEvent, type ReactElement, useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type HolidayEntry, getOrCreateProvider } from '@nodate-flow/holidays';

import EventDialog, {
  type EventDialogMode,
  type ItemKind,
} from '../features/calendar-events/event-dialog';
import PendingInvitesPanel from '../features/calendar-invites/pending-invites-panel';
import calendarLayoutStyles from '../features/calendar-invites/pending-invites-panel.module.css';
import CalendarsRail from '../features/calendars-rail/calendars-rail';
import type { Project } from '../features/projects/api';
import { useMeQuery } from '../features/settings/api';
import type { TaskDerivedState } from '../features/tasks/api';
import { STATE_COLOR } from '../features/tasks/constants';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { type ApiError, toApiError } from '../lib/api-error';
import { dateKey } from '../lib/date-utils';
import { sdk, timeSdk } from '../lib/sdk';
import { useActiveWorkspaceId } from '../lib/use-current-workspace';
import styles from './_authenticated.calendar.module.css';

/**
 * Error code emitted by the backend when a PATCH /tasks request would
 * leave `dueOn < startedOn`. Matched against `ApiError.code` to surface
 * a targeted toast instead of a generic failure message.
 */
const DUE_BEFORE_START_CODE = 'VALIDATION.BODY.DUE_BEFORE_START';

type CalendarTask = components['schemas']['MyTaskListItem'];
type CalendarEvent = timeComponents['schemas']['MyCalendarEventResponse'];
type WeekStart = 'mon' | 'sun' | 'sat';
type Weekday = 'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat' | 'sun';

/**
 * Weekday-key order indexed by the user's `weekStart` preference. The
 * column at index 0 is whichever day the user marked as the start of
 * their week; indexes 1..6 follow in calendar order.
 */
const WEEKDAY_KEYS_BY_START: Record<WeekStart, readonly Weekday[]> = {
  mon: ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'],
  sun: ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'],
  sat: ['sat', 'sun', 'mon', 'tue', 'wed', 'thu', 'fri'],
};

/** JS `Date.getDay()` value (Sun=0..Sat=6) for the weekStart anchor. */
const WEEKSTART_TO_DOW: Record<WeekStart, number> = { sun: 0, mon: 1, sat: 6 };

/**
 * Day of week as 0..6 with the user's chosen start day as 0.
 * E.g. when `weekStart === 'sun'`, Sunday → 0, Monday → 1, …, Saturday → 6.
 */
function dowFromStart(date: Date, weekStart: WeekStart): number {
  const anchor = WEEKSTART_TO_DOW[weekStart];
  return (date.getDay() - anchor + 7) % 7;
}

/**
 * Optional CSS class for the weekday header at column `index`. Looks at
 * the actual weekday key (sun → danger, sat → info), so the tint moves
 * with the user's start-of-week preference.
 */
function weekdayHeaderClass(keys: readonly Weekday[], index: number): string | undefined {
  const key = keys[index];
  if (key === 'sun') return styles['weekday--sun'];
  if (key === 'sat') return styles['weekday--sat'];
  return undefined;
}

/**
 * Date-number CSS class for a calendar cell. Holidays take precedence
 * over the Saturday "info" tint so a Saturday-holiday reads as red,
 * matching the inline holiday label. Sundays and weekday-holidays
 * both resolve to danger; weekdays return `undefined` (caller keeps
 * default colour from `.dateNumber`).
 */
function dateNumberClass(date: Date, hasHoliday: boolean): string | undefined {
  const dow = date.getDay();
  if (dow === 0 || hasHoliday) return styles['dateNumber--holiday'];
  if (dow === 6) return styles['dateNumber--sat'];
  return undefined;
}

/** Number of days in a given (year, monthIndex). */
function daysInMonth(year: number, monthIndex: number): number {
  return new Date(year, monthIndex + 1, 0).getDate();
}

interface MonthCell {
  date: Date;
  key: string;
  inMonth: boolean;
}

/**
 * Build a 6×7 month grid whose first column matches the user's
 * `weekStart`. Leading and trailing cells from adjacent months pad the
 * grid to a multiple of 7.
 */
function buildMonthGrid(year: number, monthIndex: number, weekStart: WeekStart): MonthCell[] {
  const first = new Date(year, monthIndex, 1);
  const lead = dowFromStart(first, weekStart);
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
  holidays: boolean;
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
    holidays: true,
  });

  const { data: workspaces } = useWorkspacesQuery();
  const activeWsId = useActiveWorkspaceId();
  const { data: me } = useMeQuery();
  const country = me.country;
  const selfUserId = me.id;
  // Narrow workspace shape for the rail — id + name only — so the
  // component does not depend on the full Workspace schema.
  const railWorkspaces = useMemo(
    () => workspaces.map((w) => ({ id: w.id, name: w.name })),
    [workspaces],
  );
  // `weekStart` may be undefined when the running auth-api binary predates
  // the column rollout; fall back to the schema default so the grid keeps
  // rendering against an older backend until the binary is restarted.
  const weekStart: WeekStart = me.weekStart ?? 'mon';
  const weekdayKeys = WEEKDAY_KEYS_BY_START[weekStart];

  // Memoize provider by country code so identity is stable until country
  // changes. Returns `null` when the user has not picked a country —
  // holidays are opt-in via the profile setting.
  const holidayProvider = useMemo(() => {
    if (!country) return null;
    return getOrCreateProvider(country);
  }, [country]);

  // Range that covers the full 42-cell month grid (may span adjacent months).
  const { fromDate, toDate, fromIso, toIso, rangeStart, rangeEnd } = useMemo(() => {
    const cells = buildMonthGrid(cursor.year, cursor.month, weekStart);
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
      rangeStart: start,
      rangeEnd: end,
    };
  }, [cursor, weekStart]);

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

  const cells = useMemo(
    () => buildMonthGrid(cursor.year, cursor.month, weekStart),
    [cursor, weekStart],
  );

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

  /**
   * dateKey → public holidays for the visible grid range. Empty when the
   * user has no country set or when the holidays layer is disabled.
   */
  const holidaysByDate = useMemo(() => {
    const map = new Map<string, HolidayEntry[]>();
    if (!holidayProvider || !layers.holidays) return map;
    const entries = holidayProvider.holidaysBetween(rangeStart, rangeEnd, i18n.language);
    for (const entry of entries) {
      const arr = map.get(entry.date);
      if (arr) arr.push(entry);
      else map.set(entry.date, [entry]);
    }
    return map;
  }, [holidayProvider, rangeStart, rangeEnd, i18n.language, layers.holidays]);

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
    <section className={styles.section}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('calendar.title')}</h1>
        <p className={styles.subtitle}>{t('calendar.subtitle')}</p>
      </header>

      <div className={styles.toolbar}>
        <div className={styles.monthNav}>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t('calendar.prev')}
            onClick={goPrev}
          >
            <ChevronLeft size={16} aria-hidden />
          </Button>
          <h2 className={styles.monthLabel}>{monthLabel}</h2>
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
          {country ? (
            <ToggleChip
              pressed={layers.holidays}
              onPressedChange={(v) => setLayers((s) => ({ ...s, holidays: v }))}
              color="var(--nf-color-danger)"
            >
              {t('calendar.layer.holidays')}
            </ToggleChip>
          ) : null}
        </ToggleChipGroup>
      </div>

      <div className={calendarLayoutStyles.layout}>
        <CalendarsRail workspaces={railWorkspaces} selfUserId={selfUserId} />
        <div className={styles.gridColumn}>
          <div className={styles.weekdayRow}>
            {weekdayKeys.map((wk, idx) => (
              <div key={wk} className={cx(styles.weekday, weekdayHeaderClass(weekdayKeys, idx))}>
                {t(`calendar.weekday.${wk}`)}
              </div>
            ))}
          </div>
          <div className={styles.grid}>
            {cells.map((cell) => {
              const dayTasks = byDate.get(cell.key) ?? [];
              const dayEvents = eventsByDate.get(cell.key) ?? [];
              const dayHolidays = holidaysByDate.get(cell.key) ?? [];
              const totalCount = dayTasks.length + dayEvents.length;
              const isToday = cell.key === todayKey;
              const isDragOver = dragOverKey === cell.key;
              // Render only the first holiday label on the cell (rare
              // multi-holiday days append "+N" — full list lives in the
              // `title` so it stays accessible on hover).
              const primaryHoliday = dayHolidays[0] ?? null;
              const extraHolidayCount = Math.max(0, dayHolidays.length - 1);
              const holidayTitle = dayHolidays.map((h) => h.name).join(', ');
              const dateNumberCls = dateNumberClass(cell.date, dayHolidays.length > 0);
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
                  className={cx(
                    styles.cell,
                    cell.inMonth && styles['cell--inMonth'],
                    isToday && styles['cell--today'],
                    isDragOver && styles['cell--dragOver'],
                  )}
                  title={cell.inMonth ? t('calendar.click_to_add') : undefined}
                >
                  <div className={styles.cellHeader}>
                    <span
                      className={cx(styles.dateNumber, dateNumberCls, isToday && styles.todayPill)}
                    >
                      {cell.date.getDate()}
                    </span>
                    {primaryHoliday ? (
                      <span className={styles.holidayLabel} title={holidayTitle}>
                        {extraHolidayCount > 0
                          ? t('calendar.holiday_more', {
                              name: primaryHoliday.name,
                              count: extraHolidayCount,
                            })
                          : primaryHoliday.name}
                      </span>
                    ) : totalCount > 0 ? (
                      <Badge tone="neutral" className={styles.countBadge}>
                        {totalCount}
                      </Badge>
                    ) : null}
                  </div>
                  <ul className={styles.pillList}>
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
                          className={styles.taskPill}
                          onClick={(e) => {
                            e.stopPropagation();
                          }}
                        >
                          <span
                            aria-hidden
                            className={styles.taskPill__dot}
                            style={{
                              background:
                                STATE_COLOR[task.derivedState as TaskDerivedState] ??
                                'var(--nf-color-fg-muted)',
                            }}
                          />
                          <span className={styles.taskPill__title}>{task.title}</span>
                        </Link>
                      </li>
                    ))}
                    {dayTasks.length > 3 ? (
                      <li className={styles.morePill}>
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
                            className={`${styles.eventPill}${ev.viewerAttending ? ` ${styles['eventPill--viewerAttending']}` : ''}`}
                            style={pill}
                          >
                            <span
                              aria-hidden
                              className={styles.eventPill__marker}
                              style={{ background: pill.markerColor }}
                            />
                            <span className={styles.eventPill__title}>{ev.title}</span>
                            {ev.attendeeCount > 0 ? (
                              <span
                                className={styles.eventPill__attendees}
                                aria-label={t('calendar.event_attendee_count', {
                                  count: ev.attendeeCount,
                                })}
                              >
                                <Users aria-hidden className={styles.eventPill__attendeesIcon} />
                                {ev.attendeeCount}
                              </span>
                            ) : null}
                          </button>
                        </li>
                      );
                    })}
                    {dayEvents.length > 2 ? (
                      <li className={styles.morePill}>
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
