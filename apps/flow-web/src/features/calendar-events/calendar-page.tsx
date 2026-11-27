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
import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import Popover from '@nodate-flow/ui/primitives/popover';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import {
  keepPreviousData,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { CalendarRange, ChevronLeft, ChevronRight, Layers } from 'lucide-react';
import { type ReactElement, useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import PageFrame from '../../components/page-frame';
import PageHeader from '../../components/page-header';
import { apiRequest } from '../../lib/api';
import { type ApiError, formatApiError } from '../../lib/api-error';
import { todayKey } from '../../lib/date-utils';
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
import CalendarLayerChips, { type LayerFlags } from './calendar-layer-chips';
import styles from './calendar-page.module.css';
import DayDetailSheet from './day-detail-sheet';
import EventDialog, { type EventDialogMode, type ItemKind } from './event-dialog';
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
  dateFromKey,
  dayDeltaBetweenKeys,
  directionBetween,
  type NavDirection,
  type WeekStart,
} from './lib/month-geometry';
import { usePointerDrag } from './lib/pointer-drag';
import { shiftAllDayEventDates, shiftEventDays } from './lib/shift-event';
import { useSlowPending } from './lib/use-slow-pending';
import MonthGrid from './month-grid';
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

  const layerChips = (
    <CalendarLayerChips
      layers={layers}
      onLayersChange={setLayers}
      showHolidays={Boolean(country)}
    />
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
            <MonthGrid
              cursor={cursor}
              weekStart={weekStart}
              slideClass={slideClass}
              isPending={tasksQuery.isPending || eventsQuery.isPending}
              byDate={byDate}
              eventsByDate={eventsByDate}
              holidaysByDate={holidaysByDate}
              todayKeyValue={todayKeyValue}
              locale={locale}
              drag={pointerDrag}
              onCellClick={handleCellClick}
              onDayOpen={setOpenDayKey}
              onMonthChange={(month, direction) => {
                setNavDirection(direction);
                setCursor(month);
              }}
              onEventOpen={handleEventOpen}
            />
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
