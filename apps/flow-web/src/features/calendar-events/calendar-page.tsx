/**
 * /calendar — unified cross-workspace calendar.
 *
 * Consumes two aggregated endpoints instead of per-workspace fan-out:
 *   - `GET /me/tasks-with-dates?from=&to=` — tasks with due_on
 *   - `GET /me/calendar-events?start=&end=` — calendar events
 *
 * The month grid overlays five toggleable layers: task-due, calendar
 * events, blocks, free, and milestones. Pills are moved between days by
 * a pointer drag ({@link usePointerDrag}) — a task rescheduled through
 * PATCH /tasks, an event through PATCH on its calendar row. Clicking a
 * cell opens the unified {@link EventDialog} in create mode (default
 * kind: event; shift-click shortcuts to block). Clicking an event pill
 * opens the same dialog in edit mode for that row; clicking a task pill
 * navigates to the task detail route (editing a task is out of scope
 * for the calendar dialog).
 *
 * Dragging is an addition to the dialog, never the only way to reach
 * something: every date a drag can set can also be typed.
 *
 * The grid honours the user's `me.weekStart` preference — Monday,
 * Sunday, or Saturday — for both the header order and the leading
 * blank cells. Weekend tints follow the actual weekday key at each
 * column index, so a Sunday-first or Saturday-first grid still paints
 * Sun-red / Sat-blue in the right place.
 */

import { getOrCreateProvider, type HolidayEntry } from '@nodate-flow/holidays';
import type { components } from '@nodate-flow/sdk';
import { usePresence } from '@nodate-flow/ui/hooks/use-presence';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import Popover from '@nodate-flow/ui/primitives/popover';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import {
  keepPreviousData,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { CalendarRange, ChevronLeft, ChevronRight, Layers, Users } from 'lucide-react';
import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import PageFrame from '../../components/page-frame';
import PageHeader from '../../components/page-header';
import { apiRequest } from '../../lib/api';
import { type ApiError, formatApiError } from '../../lib/api-error';
import { dateKey, todayKey } from '../../lib/date-utils';
import { useActiveWorkspaceId } from '../../lib/use-current-workspace';
import { resolveEffectiveZone } from '../../lib/use-effective-timezone';
import PendingInvitesPanel from '../calendar-invites/pending-invites-panel';
import calendarLayoutStyles from '../calendar-invites/pending-invites-panel.module.css';
import CalendarsRail from '../calendars-rail/calendars-rail';
import type { Project } from '../projects/api';
import { useMeQuery } from '../settings/api';
import type { TaskDerivedState } from '../tasks/api';
import { STATE_COLOR } from '../tasks/constants';
import { useWorkspacesQuery } from '../workspaces/api';
import { type PatchEventInput, type RecurrenceScope, useUpdateEvent } from './api';
import styles from './calendar-page.module.css';
import DayDetailSheet from './day-detail-sheet';
import EventDialog, { type EventDialogMode, type ItemKind } from './event-dialog';
import {
  eventColorStyle,
  eventKindSampleClassName,
  eventPillClassName,
  eventPillMarkerClassName,
} from './event-pill';
import LabelLegend, { deriveLegend, eventLabelKey } from './label-legend';
import type { CalendarDragPayload } from './lib/drag-payload';
import { eventDayKeys } from './lib/event-days';
import {
  type CalendarEvent,
  expandCalendarEvents,
  isRecurring,
} from './lib/expand-calendar-events';
import {
  buildCalendarRange,
  buildMonthGrid,
  dateFromKey,
  dateNumberClass,
  dayDeltaBetweenKeys,
  daysInMonth,
  directionBetween,
  dowFromStart,
  keyDayDelta,
  type MonthCell,
  type NavDirection,
  WEEKDAY_KEYS_BY_START,
  type WeekStart,
  weekdayHeaderClass,
} from './lib/month-geometry';
import { type PointerDragHandle, usePointerDrag } from './lib/pointer-drag';
import { shiftAllDayEventDates, shiftEventDays } from './lib/shift-event';
import { useSlowPending } from './lib/use-slow-pending';
import MonthScroll from './month-scroll';
import RecurringScopeDialog from './recurring-scope-dialog';

/** A refetch shorter than this draws no progress cue, so a fast load never flashes one. */
const FETCH_CUE_DELAY_MS = 300;

/**
 * Error code emitted by the backend when a PATCH /tasks request would
 * leave `dueOn < startedOn`. Matched against `ApiError.code` to surface
 * a targeted toast instead of a generic failure message.
 */
const DUE_BEFORE_START_CODE = 'VALIDATION.BODY.DUE_BEFORE_START';

type CalendarTask = components['schemas']['MyTaskListItem'];

/**
 * Shared empties for a day the maps have no entry for, so the day sheet
 * is handed the same identity on every render of an empty day rather
 * than a fresh array that invalidates everything memoized on it.
 */
const EMPTY_EVENTS: CalendarEvent[] = [];
const EMPTY_TASKS: CalendarTask[] = [];
const EMPTY_HOLIDAYS: HolidayEntry[] = [];

/** Pills a desktop cell draws before its first measurement. */
const DEFAULT_PILL_SLOTS = 5;

/**
 * Whole pill lines that fit in a cell's pill list, or null when the list
 * has no layout to read. One line is `--cal-pill-block-size` (rem) plus
 * the list's own row gap.
 */
function pillSlotsFor(list: HTMLElement): number | null {
  const height = list.clientHeight;
  if (height <= 0) return null;
  const style = getComputedStyle(list);
  const remPx = Number.parseFloat(getComputedStyle(list.ownerDocument.documentElement).fontSize);
  const pillPx = Number.parseFloat(style.getPropertyValue('--cal-pill-block-size')) * remPx;
  const gapPx = Number.parseFloat(style.rowGap) || 0;
  if (!(pillPx > 0)) return null;
  return Math.max(0, Math.floor((height + gapPx) / (pillPx + gapPx)));
}

/**
 * useIsMobile — true when the viewport is below the `md` breakpoint.
 * Mirrors the sidebar drawer hook so the calendar switches to the
 * mobile month-scroll + rail-drawer at the same width the app shell
 * collapses its navigation.
 */
function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < BP.md,
  );
  useEffect(() => {
    const mq = window.matchMedia(`(max-width: ${BP.md - 1}px)`);
    const onChange = (e: MediaQueryListEvent): void => setMobile(e.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);
  return mobile;
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
  | {
      mode: 'edit';
      event: CalendarEvent;
      /**
       * The start the series rule produced for the occurrence that was
       * clicked, in unix seconds; null for a row that does not repeat.
       *
       * Captured here because the grid is the only surface that knows
       * which instance was drawn — {@link expandCalendarEvents} rewrites
       * `startAt` per occurrence, and by the time the dialog has been
       * open for a keystroke that value is whatever the user typed.
       */
      occurrenceStart: number | null;
    };

/**
 * A move of a repeating event that is waiting on the scope question.
 *
 * Held whole rather than applied and revised: the day delta and the
 * occurrence the drag started from are both read from the gesture, and
 * cancelling the question has to leave the grid exactly as the drag
 * found it.
 */
interface PendingRecurringMove {
  event: CalendarEvent;
  /** Whole days between the pill's own day and the day it was dropped on. */
  delta: number;
  /** The occurrence's start under the series rule, in unix seconds. */
  occurrenceStart: number;
}

function CalendarRoute(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const qc = useQueryClient();
  const navigate = useNavigate();
  const today = new Date();
  const [cursor, setCursor] = useState<{ year: number; month: number }>({
    year: today.getFullYear(),
    month: today.getMonth(),
  });
  const isMobile = useIsMobile();
  const [railOpen, setRailOpen] = useState(false);
  /**
   * The day whose full list is open, as `YYYY-MM-DD`, or null.
   *
   * The phone view opens it for every tap: its chips are too small to
   * press, so the day sheet is where an event or a task is opened and
   * where a new event is created. The desktop grid operates on its cells
   * directly and opens it only from a day's "+N more", which is the one
   * way to reach the items a cell had no room to draw.
   */
  const [openDayKey, setOpenDayKey] = useState<string | null>(null);
  // Bumped by the "Today" button so the mobile month-scroll re-centers.
  const [scrollToTodaySignal, setScrollToTodaySignal] = useState(0);
  // Null until the first navigation, so the month drawn on arrival does
  // not slide in; afterwards the grid enters from the side it came from.
  const [navDirection, setNavDirection] = useState<NavDirection | null>(null);
  /** The desktop cell that holds the grid's single tab stop. */
  const [focusKey, setFocusKey] = useState<string | null>(null);
  const cellNodes = useRef(new Map<string, HTMLDivElement>());
  /**
   * How many pills one desktop cell draws. Cells have a fixed height
   * derived from the viewport, so the budget is read off one cell's pill
   * list and shared by all of them. The default holds until the first
   * measurement, and is what a DOM without layout keeps.
   */
  const [pillSlots, setPillSlots] = useState(DEFAULT_PILL_SLOTS);
  // Stable so the observer is attached once per mounted list, not once per render.
  const pillListRef = useCallback((el: HTMLUListElement | null): (() => void) | undefined => {
    if (el === null || typeof ResizeObserver === 'undefined') return undefined;
    const measure = (): void => {
      const slots = pillSlotsFor(el);
      if (slots !== null) setPillSlots(slots);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    measure();
    return () => observer.disconnect();
  }, []);
  // A cell keyboard navigation moved to, focused once it has rendered —
  // after a month change that is a cell of the next grid.
  const pendingFocusKey = useRef<string | null>(null);

  // Close the mobile rail drawer when growing past the breakpoint
  // (the Drawer primitive owns Escape / focus-trap while it is open).
  useEffect(() => {
    if (!isMobile) setRailOpen(false);
  }, [isMobile]);

  const [editTarget, setEditTarget] = useState<EditTarget | null>(null);
  // The overlays below stay mounted on what they last showed while they
  // animate out, so closing one plays its exit instead of cutting it.
  // Each new opening remounts the dialog (by `dialogSeq`) so it starts
  // from the target it was opened for.
  const [shownEditTarget, setShownEditTarget] = useState<EditTarget | null>(null);
  const [dialogSeq, setDialogSeq] = useState(0);
  if (editTarget !== null && editTarget !== shownEditTarget) {
    setShownEditTarget(editTarget);
    setDialogSeq((n) => n + 1);
  }
  const [shownDayKey, setShownDayKey] = useState<string | null>(null);
  if (openDayKey !== null && openDayKey !== shownDayKey) setShownDayKey(openDayKey);
  const [layers, setLayers] = useState<LayerFlags>({
    tasksDue: true,
    events: true,
    blocks: false,
    free: false,
    milestone: true,
    holidays: true,
  });

  // Label keys the viewer switched off; in-memory like `layers`.
  const [hiddenLabels, setHiddenLabels] = useState<ReadonlySet<string>>(() => new Set());

  const { data: workspaces } = useWorkspacesQuery();
  const activeWsId = useActiveWorkspaceId();
  const { data: me } = useMeQuery();
  const country = me.country;
  const selfUserId = me.id;
  // Which zone this surface reads and writes times in. The profile
  // setting existed, was validated, and was consulted by nothing here:
  // the grid, the pills and the dialog all used the browser's zone, so
  // someone from Tokyo working in Berlin saw Berlin days while the
  // server's reminders about the same events used Tokyo.
  const zone = resolveEffectiveZone(me.timezone, workspaces, activeWsId);
  // Narrow workspace shape for the rail — id, name, and the optional
  // country code (so the holidays-mode picker can pre-select the
  // workspace's own configured country) — without coupling the rail to
  // the full Workspace schema.
  const railWorkspaces = useMemo(
    () => workspaces.map((w) => ({ id: w.id, name: w.name, country: w.country })),
    [workspaces],
  );
  // `weekStart` may be undefined when the running auth-api binary predates
  // the column rollout; fall back to the schema default so the grid keeps
  // rendering against an older backend until the binary is restarted.
  const weekStart: WeekStart = me.weekStart ?? 'sun';
  const weekdayKeys = WEEKDAY_KEYS_BY_START[weekStart];

  // Memoize provider by country code so identity is stable until country
  // changes. Returns `null` when the user has not picked a country —
  // holidays are opt-in via the profile setting.
  const holidayProvider = useMemo(() => {
    if (!country) return null;
    return getOrCreateProvider(country);
  }, [country]);

  /**
   * How far past the drawn month the fetch reaches, in whole month
   * grids. The desktop grid draws exactly one month and navigates by
   * button, so it asks for exactly that. The mobile view scrolls a year
   * either way and moves the cursor as it goes, so it keeps the
   * neighbouring months loaded and the rows around the fold filled.
   */
  const bufferMonths = isMobile ? 1 : 0;

  // Range that covers the full 42-cell month grid (may span adjacent
  // months), widened by the buffer above.
  const { fromDate, toDate, fromIso, toIso, rangeStart, rangeEnd, holidayFrom, holidayTo } =
    useMemo(
      () => buildCalendarRange(cursor, weekStart, zone, bufferMonths),
      [cursor, weekStart, zone, bufferMonths],
    );

  // Single cross-workspace task query (flow-api /me/tasks-with-dates).
  //
  // The window is part of the key, so moving the cursor asks a question
  // that has never been answered and the data would be `undefined` until
  // it is. Holding the last window on screen costs a moment of days that
  // are one month stale and replaces the only alternative, which is no
  // days at all.
  //
  // It is the mobile view this is for. There the cursor moves with the
  // scroll, and the rows are a year long and drawn from the same data
  // whatever the cursor says — so the held window keeps every row on
  // screen populated while the next one is fetched, and dropping it
  // blanks the month under the reader's finger on every crossing. The
  // desktop grid draws the cursor month's own cells, so the days it
  // shows move with the cursor and the held window covers only the seam
  // the two grids share. Worth little there, worth a great deal here;
  // it is not a blanket win and it is not dead weight either.
  //
  // It covers the wait and nothing else: a placeholder is consulted
  // only while a query is pending, so a refusal still empties the grid.
  // That is what the message below the toolbar is for.
  const tasksQuery = useQuery({
    queryKey: ['calendar', 'me-tasks', fromDate, toDate] as const,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
    queryFn: async (): Promise<CalendarTask[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/me/tasks-with-dates', {
            params: { query: { from: fromDate, to: toDate, limit: 1000 } },
          }),
        'Failed to load dated tasks',
      );
      return data.tasks ?? [];
    },
  });

  const tasks = tasksQuery.data ?? [];

  // Single cross-workspace event query (/me/calendar-events).
  // Keyed on the window and held across a change for the same reason as
  // the task query above.
  const eventsQuery = useQuery({
    queryKey: ['calendar', 'me-events', fromIso, toIso] as const,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
    queryFn: async (): Promise<CalendarEvent[]> => {
      const data = await apiRequest(
        (client) =>
          client.GET('/me/calendar-events', { params: { query: { start: fromIso, end: toIso } } }),
        'Failed to load calendar events',
      );
      return data.events ?? [];
    },
  });

  const events = eventsQuery.data ?? [];
  const expandedEvents = useMemo(
    () => expandCalendarEvents(events, rangeStart, rangeEnd),
    [events, rangeStart, rangeEnd],
  );
  /**
   * The stored rows by public id, before expansion.
   *
   * A drawn occurrence carries the start the rule produced for it, not
   * the one the series is stored with. A `series`-scoped move therefore
   * has to shift the master's own window: shifting the occurrence's
   * would relocate the whole series to wherever this month's copy of it
   * happened to fall.
   */
  const storedEventsById = useMemo(() => {
    const map = new Map<string, CalendarEvent>();
    for (const ev of events) map.set(ev.id, ev);
    return map;
  }, [events]);

  const rescheduleMut = useMutation<
    components['schemas']['Task'],
    ApiError,
    { taskId: string; dueOn: string }
  >({
    mutationFn: async ({ taskId, dueOn }) => {
      const data = await apiRequest(
        (client) =>
          client.PATCH('/tasks/{id}', {
            params: { path: { id: taskId } },
            body: { dueOn },
          }),
        'Failed to reschedule',
      );
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

  // Event drag-to-move: shifts start/end by whole days (duration
  // preserved) through the shared events PATCH hook. A repeating row
  // arrives here only once the scope question below has been answered.
  const updateEventMut = useUpdateEvent();

  const [pendingMove, setPendingMove] = useState<PendingRecurringMove | null>(null);
  const [shownPendingMove, setShownPendingMove] = useState<PendingRecurringMove | null>(null);
  if (pendingMove !== null && pendingMove !== shownPendingMove) setShownPendingMove(pendingMove);

  /**
   * Send the event move.
   *
   * The write is pessimistic on purpose: nothing on the grid moves until
   * the server agrees, so a refusal needs no rollback and cannot leave
   * the pill somewhere the stored row never went. `scoped` is present
   * only for a repeating row whose scope has been answered.
   */
  const moveEvent = useCallback(
    (
      event: CalendarEvent,
      delta: number,
      scoped: { scope: RecurrenceScope; occurrenceStart: number } | null,
    ): void => {
      const isSeries = scoped !== null && scoped.scope === 'series';
      const source = isSeries ? (storedEventsById.get(event.id) ?? event) : event;
      // All-day rows are dates and are written as dates; timed rows are
      // instants.
      const body: PatchEventInput = {};
      if (source.allDay === true) {
        const shifted = shiftAllDayEventDates(source, delta);
        if (!shifted) return;
        body.startDate = shifted.startDate;
        body.endDate = shifted.endDate;
      } else {
        const shifted = shiftEventDays(source, delta);
        if (!shifted) return;
        body.startAt = shifted.startAt;
        body.endAt = shifted.endAt;
      }
      if (scoped !== null) {
        body.scope = scoped.scope;
        // `series` names no occurrence, so sending one would describe an
        // instance the write does not act on.
        if (!isSeries) body.occurrenceStart = scoped.occurrenceStart;
      }
      updateEventMut.mutate(
        {
          workspaceId: event.workspaceId,
          calendarId: event.calendarId,
          eventId: event.id,
          body,
        },
        {
          onSuccess: () => {
            setPendingMove(null);
            toaster.show({ tone: 'success', message: t('calendar.event_move_success') });
          },
          onError: (err) => {
            // The pill never left its day, so the refusal only has to be
            // said — and it is said with what the API refused, not with a
            // fixed sentence.
            setPendingMove(null);
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'calendar.event_move_error'),
            });
          },
        },
      );
    },
    [storedEventsById, updateEventMut, t],
  );

  const handleDrop = useCallback(
    (payload: CalendarDragPayload, cellKey: string): void => {
      if (payload.fromDate === cellKey) return;
      if (payload.type === 'task') {
        rescheduleMut.mutate({ taskId: payload.taskId, dueOn: cellKey });
        return;
      }
      // Event: shift the whole range by the day delta from its origin
      // cell to the drop cell, keeping the duration intact.
      const delta = dayDeltaBetweenKeys(payload.fromDate, cellKey);
      if (delta === 0) return;
      const event = payload.event;
      // A repeating row asks which occurrences the move reaches before
      // anything is sent. The occurrence's own start is read here, from
      // the instance the drag picked up, because the expander rewrites it
      // per occurrence and no later reader can recover which one it was.
      if (isRecurring(event) && typeof event.startAt === 'number') {
        setPendingMove({ event, delta, occurrenceStart: event.startAt });
        return;
      }
      moveEvent(event, delta, null);
    },
    [rescheduleMut, moveEvent],
  );

  const pointerDrag = usePointerDrag<CalendarDragPayload>(handleDrop);
  const dragOverKey = pointerDrag.drag?.overKey ?? null;

  // Projects per workspace, just for the quick-create project picker.
  const projectQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['calendar', 'projects', w.id] as const,
      staleTime: 60_000,
      queryFn: async (): Promise<Project[]> => {
        // The picker fans out per workspace; one that refuses simply
        // offers no projects instead of emptying the whole list.
        const data = await apiRequest(
          (client) =>
            client.GET('/workspaces/{wsId}/projects', { params: { path: { wsId: w.id } } }),
          'Failed to load projects',
          { onError: 'empty', empty: null },
        );
        return data?.projects ?? [];
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

  /**
   * Events after kind-layer filtering. Each kind gates on its own layer
   * flag; unknown kinds fall through to the generic `events` flag so the
   * UI never silently hides them.
   */
  const kindFilteredEvents = useMemo(() => {
    return expandedEvents.filter((ev) => {
      if (typeof ev.startAt !== 'number') return false;
      const k = ev.kind;
      if (k === 'block') return layers.blocks;
      if (k === 'free') return layers.free;
      if (k === 'milestone') return layers.milestone;
      return layers.events;
    });
  }, [expandedEvents, layers.events, layers.blocks, layers.free, layers.milestone]);

  /** Legend for the kind-filtered events; null when none is labelled. */
  const labelLegend = useMemo(
    () => deriveLegend(kindFilteredEvents, i18n.language),
    [kindFilteredEvents, i18n.language],
  );

  /**
   * Flat event list feeding the grid, the mobile month-scroll and the day
   * sheet: kind layers, then the label filter. Multi-day events stay whole
   * so the week-row layout can stretch them across columns. The label
   * filter only applies while the legend is shown, so a hidden key can
   * never filter events the viewer has no chip to restore.
   */
  const filteredEvents = useMemo(() => {
    if (!labelLegend || hiddenLabels.size === 0) return kindFilteredEvents;
    return kindFilteredEvents.filter((ev) => !hiddenLabels.has(eventLabelKey(ev)));
  }, [kindFilteredEvents, labelLegend, hiddenLabels]);

  /**
   * dateKey → calendar events (after kind and label filtering).
   *
   * An event is filed under every day it covers, not just the day it
   * starts. Filing it once put a five-day "Out of office" on the grid as
   * a single Monday entry and left Tuesday to Friday looking free — on
   * desktop only, because the mobile month-scroll lays the same data out
   * with {@link layoutWeek} and draws the whole bar. One account, two
   * answers to "am I away that week".
   */
  const eventsByDate = useMemo(() => {
    const map = new Map<string, CalendarEvent[]>();
    for (const ev of filteredEvents) {
      for (const key of eventDayKeys(ev, zone)) {
        const arr = map.get(key);
        if (arr) arr.push(ev);
        else map.set(key, [ev]);
      }
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => (a.startAt ?? 0) - (b.startAt ?? 0));
    }
    return map;
    // `zone` decides which day each event is filed under, so a profile
    // change has to rebuild the map. Zones are interned, so the same
    // IANA name is the same object on every render and listing it here
    // does not invalidate the memo the way a freshly built value would.
  }, [filteredEvents, zone]);

  /**
   * dateKey → public holidays for the visible grid range. Empty when the
   * user has no country set or when the holidays layer is disabled.
   */
  const holidaysByDate = useMemo(() => {
    const map = new Map<string, HolidayEntry[]>();
    if (!holidayProvider || !layers.holidays) return map;
    const entries = holidayProvider.holidaysBetween(holidayFrom, holidayTo, i18n.language);
    for (const entry of entries) {
      const arr = map.get(entry.date);
      if (arr) arr.push(entry);
      else map.set(entry.date, [entry]);
    }
    return map;
  }, [holidayProvider, holidayFrom, holidayTo, i18n.language, layers.holidays]);

  // "Today" has to be the reader's today, or the highlight lands on
  // a different cell from the one the events are filed under.
  const todayKeyValue = todayKey(zone, today);
  const monthLabel = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }).format(
        new Date(cursor.year, cursor.month, 1),
      ),
    [locale, cursor],
  );

  /**
   * What a failed window says for itself.
   *
   * A month that would not load is drawn exactly like a month with
   * nothing in it: the held window covers the wait, not the refusal —
   * a placeholder is only consulted while a query is pending, so an
   * error empties the grid — and neither query surfaces its failure
   * anywhere else. A reader cannot tell "you have nothing scheduled"
   * from "this did not load", and the second is the one they would act
   * on.
   *
   * Both queries feed one surface, so one line covers either failing,
   * and the line names the month it failed for rather than the window,
   * which is a wider span on the mobile view than anything on screen.
   */
  const loadFailed = tasksQuery.isError || eventsQuery.isError;
  const loadErrorMessage = loadFailed
    ? t('calendar.load_error.message', { month: monthLabel })
    : null;
  // The banner keeps its last message through its exit.
  const [shownLoadError, setShownLoadError] = useState(loadErrorMessage);
  if (loadErrorMessage !== null && loadErrorMessage !== shownLoadError) {
    setShownLoadError(loadErrorMessage);
  }
  const loadErrorPresence = usePresence<HTMLDivElement>(loadErrorMessage !== null);
  const isRefetching = tasksQuery.isFetching || eventsQuery.isFetching;
  const fetchPresence = usePresence<HTMLDivElement>(
    useSlowPending(isRefetching, FETCH_CUE_DELAY_MS),
  );

  const goPrev = (): void => {
    setNavDirection('prev');
    setCursor((c) => {
      const m = c.month - 1;
      return m < 0 ? { year: c.year - 1, month: 11 } : { year: c.year, month: m };
    });
  };
  const goNext = (): void => {
    setNavDirection('next');
    setCursor((c) => {
      const m = c.month + 1;
      return m > 11 ? { year: c.year + 1, month: 0 } : { year: c.year, month: m };
    });
  };
  const goToday = (): void => {
    const target = { year: today.getFullYear(), month: today.getMonth() };
    const direction = directionBetween(cursor, target);
    if (direction !== null) setNavDirection(direction);
    setCursor(target);
    // Re-center the mobile month-scroll on today's week.
    setScrollToTodaySignal((n) => n + 1);
  };

  /**
   * The cell that is the grid's tab stop: the last one keyboard focus
   * reached while it is still on screen, else today, else the first day
   * of the month. One stop rather than thirty keeps Tab moving through
   * the page; the arrow keys move between days.
   */
  const inMonthKeys = cells.filter((c) => c.inMonth).map((c) => c.key);
  const firstInMonthCell = cells.find((c) => c.inMonth);
  const tabStopKey =
    focusKey !== null && inMonthKeys.includes(focusKey)
      ? focusKey
      : inMonthKeys.includes(todayKeyValue)
        ? todayKeyValue
        : (inMonthKeys[0] ?? null);

  const cellRef =
    (key: string) =>
    (el: HTMLDivElement | null): void => {
      pointerDrag.dropCellRef(key)(el);
      if (el) cellNodes.current.set(key, el);
      else cellNodes.current.delete(key);
    };

  // Focus is the DOM's, so moving it to a cell that only exists after
  // this render is a synchronisation with something outside React.
  useLayoutEffect(() => {
    const key = pendingFocusKey.current;
    if (key === null) return;
    const node = cellNodes.current.get(key);
    if (!node) return;
    pendingFocusKey.current = null;
    node.focus();
  });

  const handleCellKeyDown = (e: KeyboardEvent<HTMLDivElement>, cell: MonthCell): void => {
    // Keys pressed on a pill inside the cell belong to the pill: Enter on
    // an event pill opens that event, not a new one on its day.
    if (e.target !== e.currentTarget) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (cell.inMonth) handleCellClick(cell.key, e.shiftKey);
      return;
    }
    let target: Date;
    if (e.key === 'PageUp' || e.key === 'PageDown') {
      const step = e.key === 'PageUp' ? -1 : 1;
      const first = new Date(cell.date.getFullYear(), cell.date.getMonth() + step, 1);
      const day = Math.min(cell.date.getDate(), daysInMonth(first.getFullYear(), first.getMonth()));
      target = new Date(first.getFullYear(), first.getMonth(), day);
    } else {
      const rtl = getComputedStyle(e.currentTarget).direction === 'rtl';
      const delta = keyDayDelta(e.key, dowFromStart(cell.date, weekStart), rtl);
      if (delta === null) return;
      target = new Date(cell.date);
      target.setDate(target.getDate() + delta);
    }
    e.preventDefault();
    const targetKey = dateKey(target);
    const targetMonth = { year: target.getFullYear(), month: target.getMonth() };
    const direction = directionBetween(cursor, targetMonth);
    if (direction !== null) {
      setNavDirection(direction);
      setCursor(targetMonth);
    }
    setFocusKey(targetKey);
    pendingFocusKey.current = targetKey;
  };

  /**
   * Follow the month the mobile view has scrolled to.
   *
   * `cursor` is the single source of truth for both the fetch window and
   * the toolbar label, and the scroll is the only thing that moves the
   * mobile view between months — left where it started, every month but
   * the first would be drawn from a window that was never fetched.
   * Returning the same state object when the month already matches keeps
   * a scroll within one month from re-rendering the route at all.
   */
  const handleVisibleMonthChange = useCallback((monthKey: string): void => {
    const [year, month] = monthKey.split('-').map(Number);
    if (!year || !month) return;
    const monthIndex = month - 1;
    setCursor((c) => (c.year === year && c.month === monthIndex ? c : { year, month: monthIndex }));
  }, []);

  const stateColor = useCallback(
    (derivedState: string): string =>
      STATE_COLOR[derivedState as TaskDerivedState] ?? 'var(--nf-color-fg-muted)',
    [],
  );

  const dayFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', weekday: 'short' }),
    [locale],
  );
  const cellDayFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'full' }),
    [locale],
  );
  const monthKey = `${cursor.year}-${cursor.month}`;
  const slideClass =
    navDirection === 'prev'
      ? styles['slide--fromPrev']
      : navDirection === 'next'
        ? styles['slide--fromNext']
        : undefined;

  const dragPayload = pointerDrag.drag?.payload ?? null;
  // The copy outlives the gesture by one exit, so it keeps the last payload.
  const [proxyPayload, setProxyPayload] = useState(dragPayload);
  if (dragPayload !== null && dragPayload !== proxyPayload) setProxyPayload(dragPayload);
  const proxyPresence = usePresence<HTMLDivElement>(dragPayload !== null);
  const presenceRef = proxyPresence.ref;
  const proxyRef = pointerDrag.proxyRef;
  const setProxyNode = (node: HTMLDivElement | null): void => {
    proxyRef.current = node;
    presenceRef(node);
  };
  /**
   * What the moving copy says about where it would land. A finger covers
   * part of the grid, so the day the drop would reach is named on the
   * copy rather than left to the cell highlight alone; over nothing it
   * says so, which is also how a drag is called off.
   */
  const dragTargetLabel =
    pointerDrag.drag === null
      ? null
      : pointerDrag.drag.overKey === null
        ? t('calendar.drag.no_target')
        : t('calendar.drag.to_date', {
            date: dayFormat.format(dateFromKey(pointerDrag.drag.overKey) ?? today),
          });

  const handleEventOpen = useCallback((event: CalendarEvent): void => {
    // The pill carries the instant the expander derived for this
    // instance, so reading it at the click — before the dialog exists,
    // let alone can edit anything — is what ties a per-occurrence write
    // to the occurrence the user actually opened.
    const occurrenceStart =
      isRecurring(event) && typeof event.startAt === 'number' ? event.startAt : null;
    setEditTarget({ mode: 'edit', event, occurrenceStart });
  }, []);

  const handleTaskOpen = useCallback(
    (task: CalendarTask): void => {
      void navigate({ to: '/tasks/$taskId', params: { taskId: task.id } });
    },
    [navigate],
  );

  // Quiet tinted chips: the swatch carries each layer's colour, so the
  // toggles do not outshout the grid they filter.
  const layerChips = (
    <ToggleChipGroup label={t('calendar.layers')}>
      <ToggleChip
        pressed={layers.tasksDue}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, tasksDue: v }))}
        color="var(--nf-cal-task-color)"
      >
        {t('calendar.layer.tasks_due')}
      </ToggleChip>
      <ToggleChip
        pressed={layers.events}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, events: v }))}
        label={t('calendar.layer.events')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="event" />
          {t('calendar.layer.events')}
        </span>
      </ToggleChip>
      <ToggleChip
        pressed={layers.blocks}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, blocks: v }))}
        label={t('calendar.layer.blocks')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="block" />
          {t('calendar.layer.blocks')}
        </span>
      </ToggleChip>
      <ToggleChip
        pressed={layers.free}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, free: v }))}
        label={t('calendar.layer.free')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="free" />
          {t('calendar.layer.free')}
        </span>
      </ToggleChip>
      <ToggleChip
        pressed={layers.milestone}
        tone="tint"
        onPressedChange={(v) => setLayers((s) => ({ ...s, milestone: v }))}
        label={t('calendar.layer.milestone')}
      >
        <span className={styles.kindChip}>
          <span aria-hidden className={eventKindSampleClassName} data-kind="milestone" />
          {t('calendar.layer.milestone')}
        </span>
      </ToggleChip>
      {country ? (
        <ToggleChip
          pressed={layers.holidays}
          tone="tint"
          onPressedChange={(v) => setLayers((s) => ({ ...s, holidays: v }))}
          color="var(--nf-color-danger)"
        >
          {t('calendar.layer.holidays')}
        </ToggleChip>
      ) : null}
    </ToggleChipGroup>
  );

  return (
    <PageFrame width="full" className={styles.section}>
      <PageHeader title={t('calendar.title')} help="calendar" />

      <div className={styles.toolbar}>
        <div className={styles.monthNav}>
          <h2 className={styles.monthLabel} aria-live="polite">
            {/* Keyed so the label settles in with the grid rather than
                swapping under it. */}
            <span key={monthKey} className={cx(styles.monthLabel__text, slideClass)}>
              {monthLabel}
            </span>
          </h2>
          {/* One control group, so the three steps share a size and a rhythm. */}
          <div className={styles.navGroup}>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className={styles.navButton}
              aria-label={t('calendar.prev')}
              onClick={goPrev}
            >
              <ChevronLeft size={16} aria-hidden />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className={styles.navButton}
              onClick={goToday}
            >
              {t('calendar.today')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className={styles.navButton}
              aria-label={t('calendar.next')}
              onClick={goNext}
            >
              <ChevronRight size={16} aria-hidden />
            </Button>
          </div>
        </div>

        {/* The wrapper takes the tooltip: the group does not forward handlers.
            On a phone the chips fold into a popover so they do not push the
            grid down by two rows. */}
        <div className={styles.filters}>
          {isMobile ? null : (
            <Tooltip content={t('calendar.layerHint')}>
              <div className={styles.layers}>{layerChips}</div>
            </Tooltip>
          )}
          {labelLegend ? (
            <LabelLegend
              legend={labelLegend}
              hidden={hiddenLabels}
              onHiddenChange={setHiddenLabels}
            />
          ) : null}
        </div>
      </div>

      {/* Mobile-only trigger to open the calendars rail as a drawer. */}
      {isMobile ? (
        <div className={styles.mobileRailBar}>
          <Popover
            content={<div className={styles.layersPopover}>{layerChips}</div>}
            placement="bottom-end"
            ariaLabel={t('calendar.layers')}
          >
            <Button type="button" variant="ghost" size="sm">
              <Layers size={16} aria-hidden />
              {t('calendar.layers')}
            </Button>
          </Popover>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setRailOpen(true)}
            aria-haspopup="dialog"
            aria-expanded={railOpen}
          >
            <CalendarRange size={16} aria-hidden />
            {t('calendars_rail.title')}
          </Button>
        </div>
      ) : null}

      <div className={cx(calendarLayoutStyles.layout, styles.layoutFill)}>
        {isMobile ? null : <CalendarsRail workspaces={railWorkspaces} selfUserId={selfUserId} />}
        <div className={styles.gridColumn}>
          {/* Above the grid rather than in a toast: the window is
              refetched on every month the scroll crosses, so a dead
              network would raise one toast per crossing. It goes away
              on its own the moment a refetch returns. */}
          {fetchPresence.mounted ? (
            <div
              ref={fetchPresence.ref}
              data-state={fetchPresence.state}
              className={styles.fetchBar}
              aria-hidden
            >
              <div className={styles.fetchBar__runner} />
            </div>
          ) : null}
          {loadErrorPresence.mounted && shownLoadError !== null ? (
            <div
              ref={loadErrorPresence.ref}
              data-state={loadErrorPresence.state}
              className={styles.loadError}
              role="status"
            >
              <p className={styles.loadError__message}>{shownLoadError}</p>
              <Button
                type="button"
                variant="default"
                size="sm"
                onClick={() => {
                  void tasksQuery.refetch();
                  void eventsQuery.refetch();
                }}
              >
                {t('calendar.load_error.retry')}
              </Button>
            </div>
          ) : null}
          {isMobile ? (
            <MonthScroll
              events={filteredEvents}
              tasksByDate={byDate}
              holidaysByDate={holidaysByDate}
              locale={locale}
              weekStart={weekStart}
              zone={zone}
              stateColor={stateColor}
              scrollToTodaySignal={scrollToTodaySignal}
              onVisibleMonthChange={handleVisibleMonthChange}
              // The same gesture the desktop grid presses, so a move made
              // on a phone raises the same scope question, sends the same
              // request, and reports the same refusal.
              drag={pointerDrag}
              onDayOpen={setOpenDayKey}
            />
          ) : (
            <>
              <div className={styles.weekdayRow}>
                {weekdayKeys.map((wk, idx) => (
                  <div
                    key={wk}
                    className={cx(styles.weekday, weekdayHeaderClass(weekdayKeys, idx))}
                  >
                    {t(`calendar.weekday.${wk}`)}
                  </div>
                ))}
              </div>
              {/* Keyed by month, so a navigation mounts a fresh grid that
                  slides in from the side it came from. A refetch keeps
                  the key, and the arrival month has no direction yet. */}
              <div
                key={monthKey}
                className={cx(styles.grid, slideClass)}
                // The week count sets the row tracks the remaining height is shared by.
                style={{ '--cal-weeks': cells.length / 7 } as CSSProperties}
                aria-busy={tasksQuery.isPending || eventsQuery.isPending || undefined}
              >
                {cells.map((cell) => {
                  const dayTasks = byDate.get(cell.key) ?? [];
                  const dayEvents = eventsByDate.get(cell.key) ?? [];
                  const dayHolidays = holidaysByDate.get(cell.key) ?? [];
                  const totalCount = dayTasks.length + dayEvents.length;
                  // The cell draws as many pills as its fixed height holds,
                  // tasks first; the rest fold into the header's "+N more".
                  const visibleTasks = dayTasks.slice(0, pillSlots);
                  const visibleEvents = dayEvents.slice(
                    0,
                    Math.max(0, pillSlots - visibleTasks.length),
                  );
                  const hiddenCount = totalCount - visibleTasks.length - visibleEvents.length;
                  const isToday = cell.key === todayKeyValue;
                  const isDragOver = dragOverKey === cell.key;
                  // Render only the first holiday label on the cell (rare
                  // multi-holiday days append "+N" — full list lives in the
                  // `title` so it stays accessible on hover).
                  const primaryHoliday = dayHolidays[0] ?? null;
                  const extraHolidayCount = Math.max(0, dayHolidays.length - 1);
                  const holidayTitle = dayHolidays.map((h) => h.name).join(', ');
                  const dateNumberCls = dateNumberClass(cell.date, dayHolidays.length > 0);
                  return (
                    // A cell carries its handlers whether or not it is a
                    // day of the month on screen, but only a day of the
                    // month is given a role — a leading or trailing cell
                    // is filler, and both handlers no-op on one. The rule
                    // reads the role as an expression rather than a value
                    // and so cannot see the one the day cells get, and it
                    // is right about the filler ones either way.
                    // biome-ignore lint/a11y/noStaticElementInteractions: role is conditional on `cell.inMonth`; the handlers below are inert for the cells that have none.
                    // biome-ignore lint/a11y/useAriaPropsSupportedByRole: same — the label is set only alongside the conditional `button` role.
                    <div
                      key={cell.key}
                      // Registers the cell as a drop target: the gesture
                      // hit-tests these rects rather than asking the DOM
                      // what is under the pointer, which a finger — whose
                      // events stay aimed at the element it started on —
                      // would answer wrongly.
                      ref={cellRef(cell.key)}
                      // data-cell-key exposes the YYYY-MM-DD cell address for
                      // E2E drag-target selection (otherwise CSS-module hashes
                      // make cells un-addressable).
                      data-cell-key={cell.key}
                      onClick={(e) => {
                        if ((e.target as HTMLElement).closest('a, button')) return;
                        if (cell.inMonth) handleCellClick(cell.key, e.shiftKey);
                      }}
                      onKeyDown={(e) => handleCellKeyDown(e, cell)}
                      onFocus={(e) => {
                        if (e.target === e.currentTarget && cell.inMonth) setFocusKey(cell.key);
                      }}
                      role={cell.inMonth ? 'button' : undefined}
                      tabIndex={cell.inMonth ? (cell.key === tabStopKey ? 0 : -1) : undefined}
                      aria-label={
                        cell.inMonth
                          ? primaryHoliday
                            ? t('calendar.month_scroll.day_label_holiday', {
                                date: cellDayFormat.format(cell.date),
                                holiday: primaryHoliday.name,
                                count: totalCount,
                              })
                            : t('calendar.month_scroll.day_label', {
                                date: cellDayFormat.format(cell.date),
                                count: totalCount,
                              })
                          : undefined
                      }
                      aria-current={isToday ? 'date' : undefined}
                      className={cx(
                        styles.cell,
                        cell.inMonth && styles['cell--inMonth'],
                        isToday && styles['cell--today'],
                        isDragOver && styles['cell--dragOver'],
                      )}
                    >
                      <div className={styles.cellHeader}>
                        <span
                          className={cx(
                            styles.dateNumber,
                            dateNumberCls,
                            isToday && styles.todayPill,
                          )}
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
                        ) : null}
                        {/* In the header rather than on a line of its own, so
                            every line below it is a pill. One count for what
                            the cell had no room for, and a way to reach it:
                            the day sheet lists the whole day. */}
                        {hiddenCount > 0 ? (
                          <button
                            type="button"
                            className={styles.morePill}
                            onClick={(e) => {
                              e.stopPropagation();
                              setOpenDayKey(cell.key);
                            }}
                          >
                            {t('calendar.more', { count: hiddenCount })}
                          </button>
                        ) : null}
                      </div>
                      <DayPillList
                        tasks={visibleTasks}
                        events={visibleEvents}
                        cellKey={cell.key}
                        drag={pointerDrag}
                        onEventOpen={handleEventOpen}
                        {...(cell === firstInMonthCell ? { listRef: pillListRef } : {})}
                      />
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </div>

        {isMobile ? null : <PendingInvitesPanel />}
      </div>

      {/* Mobile calendars-rail drawer — reuses the Drawer primitive for
          focus trap, overlay lock, Escape, and backdrop a11y. */}
      {isMobile ? (
        <Drawer
          open={railOpen}
          onClose={() => setRailOpen(false)}
          title={t('calendars_rail.title')}
          side="inline-end"
        >
          <CalendarsRail workspaces={railWorkspaces} selfUserId={selfUserId} />
        </Drawer>
      ) : null}

      {/* The day open in full. Each handler closes it first:
          what it opens is a dialog or another route, and a sheet left
          standing behind either is a surface nobody asked to keep. */}
      {shownDayKey !== null ? (
        <DayDetailSheet
          open={openDayKey !== null}
          side={isMobile ? 'block-end' : 'inline-end'}
          dateKey={shownDayKey}
          locale={locale}
          zone={zone}
          events={eventsByDate.get(shownDayKey) ?? EMPTY_EVENTS}
          tasks={byDate.get(shownDayKey) ?? EMPTY_TASKS}
          holidays={holidaysByDate.get(shownDayKey) ?? EMPTY_HOLIDAYS}
          stateColor={stateColor}
          onClose={() => setOpenDayKey(null)}
          onEventOpen={(event) => {
            setOpenDayKey(null);
            handleEventOpen(event);
          }}
          onTaskOpen={(task) => {
            setOpenDayKey(null);
            handleTaskOpen(task);
          }}
          onCreate={(key) => {
            setOpenDayKey(null);
            handleCellClick(key, false);
          }}
        />
      ) : null}

      {shownEditTarget !== null ? (
        <EventDialog
          key={dialogSeq}
          open={editTarget !== null}
          zone={zone}
          workspaceId={
            shownEditTarget.mode === 'edit' ? shownEditTarget.event.workspaceId : (activeWsId ?? '')
          }
          projects={allProjects}
          mode={toDialogMode(shownEditTarget)}
          onClose={() => setEditTarget(null)}
          onSaved={handleSaved}
        />
      ) : null}

      {/* A repeating event dropped on another day asks the same question
          the edit dialog asks before it saves. Cancelling sends nothing:
          the pill never left its day, because nothing here moves until
          the server has agreed. */}
      {shownPendingMove !== null ? (
        <RecurringScopeDialog
          // Remounted per move, so each question starts from its default.
          key={`${shownPendingMove.event.id}@${shownPendingMove.occurrenceStart}:${shownPendingMove.delta}`}
          open={pendingMove !== null}
          action="save"
          pending={updateEventMut.isPending}
          onCancel={() => setPendingMove(null)}
          onConfirm={(scope) => {
            if (pendingMove === null) return;
            moveEvent(pendingMove.event, pendingMove.delta, {
              scope,
              occurrenceStart: pendingMove.occurrenceStart,
            });
          }}
        />
      ) : null}

      {/* The moving copy. It is portalled out of the grid because a cell
          clips its own overflow, and positioned by the gesture itself so
          a pointer that moves every frame does not re-render the month
          every frame. */}
      {proxyPresence.mounted && proxyPayload !== null
        ? createPortal(
            <div
              ref={setProxyNode}
              data-state={proxyPresence.state}
              className={styles.dragProxy}
              aria-hidden
            >
              <span
                className={cx(
                  styles.dragProxy__marker,
                  // A task is marked by a dot and an event by a diamond
                  // on the grid; the copy keeps whichever it came from,
                  // so what is moving stays recognisable.
                  proxyPayload.type === 'task' && styles['dragProxy__marker--dot'],
                )}
                style={{ background: proxyPayload.dotColor }}
              />
              <span className={styles.dragProxy__title}>{proxyPayload.label}</span>
              {dragPayload !== null && dragTargetLabel !== null ? (
                <span className={styles.dragProxy__target}>{dragTargetLabel}</span>
              ) : null}
            </div>,
            document.body,
          )
        : null}
      <p aria-live="polite" className={styles.srOnly}>
        {dragTargetLabel ?? ''}
      </p>
    </PageFrame>
  );
}

/** One pill a desktop cell draws: a task due that day, or an event covering it. */
type DayPill = { kind: 'task'; task: CalendarTask } | { kind: 'event'; event: CalendarEvent };

interface DayPillListProps {
  cellKey: string;
  /** The tasks the cell has room for, already cut to fit. */
  tasks: CalendarTask[];
  /** The events the cell has room for, already cut to fit. */
  events: CalendarEvent[];
  drag: PointerDragHandle<CalendarDragPayload>;
  onEventOpen: (event: CalendarEvent) => void;
  /** Set on one cell only: the list the grid measures to size every cell's pill budget. */
  listRef?: (el: HTMLUListElement | null) => (() => void) | undefined;
}

/**
 * The pills of one desktop cell.
 *
 * Rendered through {@link usePresenceList}, so a pill that leaves the day
 * — deleted, moved by a drop, hidden by a layer or calendar toggle —
 * collapses and fades instead of vanishing, and one that arrives slides
 * in. A refetch with the same members keeps every node and plays nothing.
 */
function DayPillList({
  cellKey,
  tasks,
  events,
  drag: pointerDrag,
  onEventOpen,
  listRef,
}: DayPillListProps): ReactElement {
  const { t } = useTranslation('common');
  const pills: DayPill[] = [
    ...tasks.map((task): DayPill => ({ kind: 'task', task })),
    ...events.map((event): DayPill => ({ kind: 'event', event })),
  ];
  const { entries, onExited } = usePresenceList(pills, (p) =>
    p.kind === 'task' ? `task-${p.task.id}` : `ev-${p.event.id}`,
  );

  return (
    <ul ref={listRef} className={styles.pillList}>
      {entries.map(({ key, item, present }) => (
        <PresenceItem
          key={key}
          as="li"
          present={present}
          onExited={() => onExited(key)}
          className={motion.enter}
        >
          {item.kind === 'task' ? renderTaskPill(item.task) : renderEventPill(item.event)}
        </PresenceItem>
      ))}
    </ul>
  );

  function renderTaskPill(task: CalendarTask): ReactElement {
    const sourceKey = `task-${task.id}@${cellKey}`;
    const dotColor =
      STATE_COLOR[task.derivedState as TaskDerivedState] ?? 'var(--nf-color-fg-muted)';
    return (
      <Link
        to="/tasks/$taskId"
        params={{ taskId: task.id }}
        title={`${task.title} · ${task.workspaceName}\n${t('calendar.drag_to_reschedule')}`}
        // The browser drags a link's URL of its own
        // accord, which would run alongside this
        // gesture and drop a hyperlink somewhere.
        draggable={false}
        onPointerDown={(e) => {
          pointerDrag.pressSource(e, sourceKey, {
            type: 'task',
            taskId: task.id,
            fromDate: cellKey,
            label: task.title,
            dotColor,
          });
        }}
        className={cx(
          styles.taskPill,
          pointerDrag.holdingKey === sourceKey && styles['pill--holding'],
          pointerDrag.drag?.sourceKey === sourceKey && styles['pill--lifted'],
        )}
        onClick={(e) => {
          e.stopPropagation();
          // The release that ended a drag becomes a
          // click on the source; navigating on it
          // would take the user off the calendar
          // every time they moved a task.
          if (pointerDrag.wasDragged()) e.preventDefault();
        }}
      >
        <span aria-hidden className={styles.taskPill__dot} style={{ background: dotColor }} />
        <span className={styles.taskPill__title}>{task.title}</span>
      </Link>
    );
  }

  function renderEventPill(ev: CalendarEvent): ReactElement {
    const sourceKey = `ev-${ev.id}@${cellKey}`;
    return (
      <button
        type="button"
        title={`${ev.title} · ${ev.workspaceName}\n${t('calendar.drag_to_reschedule')}`}
        aria-label={
          ev.labelName
            ? t('calendar.event_detail.open_label_with_label', {
                title: ev.title,
                label: ev.labelName,
                workspace: ev.workspaceName,
              })
            : t('calendar.event_detail.open_label', {
                title: ev.title,
                workspace: ev.workspaceName,
              })
        }
        onPointerDown={(e) => {
          pointerDrag.pressSource(e, sourceKey, {
            type: 'event',
            event: ev,
            fromDate: cellKey,
            label: ev.title,
            dotColor: ev.color,
          });
        }}
        onClick={(e) => {
          e.stopPropagation();
          // A drag ends in a click on the pill it
          // started from; opening the dialog then
          // would bury the move under a form.
          if (pointerDrag.wasDragged()) return;
          onEventOpen(ev);
        }}
        className={cx(
          styles.eventPill,
          eventPillClassName(ev.kind),
          ev.viewerAttending && styles['eventPill--viewerAttending'],
          styles['eventPill--draggable'],
          pointerDrag.holdingKey === sourceKey && styles['pill--holding'],
          pointerDrag.drag?.sourceKey === sourceKey && styles['pill--lifted'],
        )}
        style={eventColorStyle(ev.color)}
      >
        <span aria-hidden className={cx(styles.eventPill__marker, eventPillMarkerClassName)} />
        <span className={styles.eventPill__title}>{ev.title}</span>
        {ev.attendeeCount > 0 ? (
          <span
            role="img"
            className={styles.eventPill__attendees}
            aria-label={t('calendar.event_attendee_count', { count: ev.attendeeCount })}
          >
            <Users aria-hidden className={styles.eventPill__attendeesIcon} />
            {ev.attendeeCount}
          </span>
        ) : null}
      </button>
    );
  }
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
    // Omitted rather than nulled for a row that does not repeat: the
    // dialog reads the member's absence as "there is no occurrence to
    // scope a write to", and offers no extra step.
    ...(target.occurrenceStart === null
      ? {}
      : { occurrence: { originalStartAt: target.occurrenceStart } }),
  };
}

export default CalendarRoute;
