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
import { expandAllRecurrences, type RecurrenceRule } from '@nodate-flow/ui/calendar';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import { Zone } from '@nodate-flow/ui/time';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import {
  keepPreviousData,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { CalendarRange, ChevronLeft, ChevronRight, Users } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';

import {
  type PatchEventInput,
  type RecurrenceScope,
  useUpdateEvent,
} from '../features/calendar-events/api';
import DayDetailSheet from '../features/calendar-events/day-detail-sheet';
import EventDialog, {
  type EventDialogMode,
  type ItemKind,
} from '../features/calendar-events/event-dialog';
import type { CalendarDragPayload } from '../features/calendar-events/lib/drag-payload';
import { eventDayKeys } from '../features/calendar-events/lib/event-days';
import { usePointerDrag } from '../features/calendar-events/lib/pointer-drag';
import { shiftEventDays } from '../features/calendar-events/lib/shift-event';
import MonthScroll from '../features/calendar-events/month-scroll';
import RecurringScopeDialog from '../features/calendar-events/recurring-scope-dialog';
import PendingInvitesPanel from '../features/calendar-invites/pending-invites-panel';
import calendarLayoutStyles from '../features/calendar-invites/pending-invites-panel.module.css';
import CalendarsRail from '../features/calendars-rail/calendars-rail';
import type { Project } from '../features/projects/api';
import { useMeQuery } from '../features/settings/api';
import type { TaskDerivedState } from '../features/tasks/api';
import { STATE_COLOR } from '../features/tasks/constants';
import { useWorkspacesQuery } from '../features/workspaces/api';
import { apiRequest } from '../lib/api';
import { type ApiError, formatApiError } from '../lib/api-error';
import { dateKey, endOfDayIso, startOfDayIso, todayKey } from '../lib/date-utils';
import { useActiveWorkspaceId } from '../lib/use-current-workspace';
import { resolveEffectiveZone } from '../lib/use-effective-timezone';
import styles from './_authenticated.calendar.module.css';

/**
 * Error code emitted by the backend when a PATCH /tasks request would
 * leave `dueOn < startedOn`. Matched against `ApiError.code` to surface
 * a targeted toast instead of a generic failure message.
 */
const DUE_BEFORE_START_CODE = 'VALIDATION.BODY.DUE_BEFORE_START';

type CalendarTask = components['schemas']['MyTaskListItem'];
type CalendarEvent = components['schemas']['MyCalendarEventResponse'];
type RecurrenceExpansionInput<T> = {
  event: T;
  startAt: string;
  endAt: string;
  timezone: string;
  recurrenceRule: RecurrenceRule | null;
  recurrenceExceptions?: string[];
  overriddenStarts?: string[];
  recurrenceEnd?: string;
};
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
 * Shared empties for a day the maps have no entry for, so the day sheet
 * is handed the same identity on every render of an empty day rather
 * than a fresh array that invalidates everything memoized on it.
 */
const EMPTY_EVENTS: CalendarEvent[] = [];
const EMPTY_TASKS: CalendarTask[] = [];
const EMPTY_HOLIDAYS: HolidayEntry[] = [];

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

/** The windows one calendar view fetches and draws from. */
export interface CalendarViewRange {
  /** Task query bounds — inclusive `YYYY-MM-DD` day keys. */
  fromDate: string;
  toDate: string;
  /** Event query bounds — the same days as instants, read in `zone`. */
  fromIso: string;
  toIso: string;
  /** The event bounds as `Date`s, for the recurrence expander. */
  rangeStart: Date;
  rangeEnd: Date;
  /** Holiday provider bounds, in local-component `Date`s. */
  holidayFrom: Date;
  holidayTo: Date;
}

/**
 * Every window the calendar reads, derived from the month at the cursor.
 *
 * `bufferMonths` widens the window by that many whole month grids on
 * each side. The mobile month view scrolls through months freely, and a
 * row reaches the top before the cursor that follows it can be fetched —
 * so the months on either side are asked for in advance, or they arrive
 * empty and fill in after the fact.
 *
 * All four windows are derived from one pair of days on purpose. A fetch
 * that reaches further than the expansion window loads occurrences the
 * expander then drops, and a holiday window that lags leaves the extra
 * months unmarked; either one is invisible until someone scrolls there.
 *
 * The grid's own cells are local-component Dates, so their day keys
 * round-trip in whatever frame they were built in. The instants sent as
 * `start`/`end` are a different question: they are the bounds of "the
 * days on screen", and a day boundary only exists relative to a zone.
 * Taken from the browser they ask the server for a window offset from
 * the one being drawn, so events at the first and last cell are fetched
 * for the wrong day — or, for a viewer far enough east or west, not
 * fetched at all.
 */
export function buildCalendarRange(
  cursor: { year: number; month: number },
  weekStart: WeekStart,
  zone: Zone,
  bufferMonths: number,
): CalendarViewRange {
  // `Date` normalises an out-of-range month index, so a buffer that
  // crosses a year boundary needs no special case.
  const leadingCells = buildMonthGrid(cursor.year, cursor.month - bufferMonths, weekStart);
  const trailingCells =
    bufferMonths === 0
      ? leadingCells
      : buildMonthGrid(cursor.year, cursor.month + bufferMonths, weekStart);
  const fallback = new Date(cursor.year, cursor.month, 1);
  const first = leadingCells[0]?.date ?? fallback;
  const last = trailingCells[trailingCells.length - 1]?.date ?? fallback;
  const firstKey = dateKey(first);
  const lastKey = dateKey(last);
  const startIso = startOfDayIso(firstKey, zone);
  const endIso = endOfDayIso(lastKey, zone);
  // The holiday window stays in local-component Dates because the
  // provider compares against local-component Dates of its own. A
  // holiday is a date, not an instant, so both sides being in the same
  // arbitrary frame is what makes the comparison mean "the same square"
  // — feeding it the zoned instants above would drop or add one at the
  // edge of the grid.
  const holidayStart = new Date(first);
  holidayStart.setHours(0, 0, 0, 0);
  const holidayEnd = new Date(last);
  holidayEnd.setHours(23, 59, 59, 999);
  return {
    fromDate: firstKey,
    toDate: lastKey,
    fromIso: startIso,
    toIso: endIso,
    rangeStart: new Date(startIso),
    rangeEnd: new Date(endIso),
    holidayFrom: holidayStart,
    holidayTo: holidayEnd,
  };
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
        // The hatch has to darken a light canvas and lighten a dark one,
        // so it is mixed from the theme's foreground rather than written
        // as black: a flat 4% black is invisible on the dark themes.
        backgroundImage:
          'repeating-linear-gradient(135deg, transparent 0 6px, color-mix(in oklch, var(--nf-color-fg) 4%, transparent) 6px 8px)',
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

/**
 * True when an event carries a recurrence rule.
 *
 * Dropping such a pill on another day raises the same "this occurrence /
 * this and following / the whole series" question the edit dialog asks
 * before it saves, so a person meets one question however they move an
 * event. Until a per-occurrence write existed these pills declined the
 * drag outright, because a master shifted in place silently rewrites
 * every occurrence it produces.
 */
function isRecurring(event: CalendarEvent): boolean {
  const rule = event.recurrenceRule;
  if (rule == null) return false;
  if (typeof rule === 'string') return rule.trim().length > 0;
  // Non-string recurrence payloads (object form) count as recurring.
  return true;
}

function recurrenceRule(value: unknown): RecurrenceRule | null {
  if (!value || typeof value !== 'object') return null;
  const freq = (value as { freq?: unknown }).freq;
  if (freq !== 'daily' && freq !== 'weekly' && freq !== 'monthly' && freq !== 'yearly') return null;
  return value as RecurrenceRule;
}

/**
 * Read a list of occurrence starts off an event row.
 *
 * `recurrenceExceptions` and `overriddenStarts` are the same wire shape —
 * an array of RFC 3339 instants or bare `YYYY-MM-DD` days — and the
 * expander reads both through one parser, so they are narrowed here by
 * one function too. `recurrenceExceptions` is typed `unknown` by the
 * generated SDK, hence the runtime check rather than a cast.
 */
function occurrenceStarts(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const strings = value.filter((v): v is string => typeof v === 'string');
  return strings.length > 0 ? strings : undefined;
}

function secondsToIso(seconds: number): string {
  return new Date(seconds * 1000).toISOString();
}

function isoToSeconds(iso: string): number | undefined {
  const millis = Date.parse(iso);
  if (Number.isNaN(millis)) return undefined;
  return Math.floor(millis / 1000);
}

/**
 * Turn the stored rows from `/me/calendar-events` into the occurrences the
 * grid draws: recurring masters expand into instances, everything else —
 * including an override row, which carries no rule of its own — passes
 * through unchanged.
 */
export function expandCalendarEvents(
  events: CalendarEvent[],
  rangeStart: Date,
  rangeEnd: Date,
): CalendarEvent[] {
  const recurrenceInput: RecurrenceExpansionInput<CalendarEvent>[] = events.map((event) => {
    const rule = recurrenceRule(event.recurrenceRule);
    const exceptions = occurrenceStarts(event.recurrenceExceptions);
    const overridden = occurrenceStarts(event.overriddenStarts);
    const input: RecurrenceExpansionInput<CalendarEvent> = {
      event,
      startAt: typeof event.startAt === 'number' ? secondsToIso(event.startAt) : '',
      endAt:
        typeof event.endAt === 'number'
          ? secondsToIso(event.endAt)
          : typeof event.startAt === 'number'
            ? secondsToIso(event.startAt)
            : '',
      timezone: event.timezone,
      recurrenceRule: rule,
    };
    if (exceptions) input.recurrenceExceptions = exceptions;
    // The occurrences an override row already stands in for. Dropped here
    // the master keeps emitting the original occurrence while the override
    // row draws its moved copy, so one occurrence appears twice.
    if (overridden) input.overriddenStarts = overridden;
    // recurrence_end is the second upper bound on a series; without it a
    // series the API was told to stop keeps being drawn.
    if (typeof event.recurrenceEnd === 'number') {
      input.recurrenceEnd = secondsToIso(event.recurrenceEnd);
    }
    return input;
  });

  // The window bounds are instants and the expander only ever compares
  // them as instants, so the zone they are read in cannot change which
  // occurrences fall inside. Naming UTC rather than letting them adopt
  // the host zone keeps that true by construction instead of by
  // coincidence: the moment a bound is used for anything day-shaped,
  // an unnamed zone would quietly make the window the reader's.
  const utc = Zone.utc().name;
  return expandAllRecurrences(
    recurrenceInput,
    DateTime.fromJSDate(rangeStart, { zone: utc }),
    DateTime.fromJSDate(rangeEnd, { zone: utc }).plus({ milliseconds: 1 }),
  ).map((instance) => ({
    ...instance.event,
    ...((isoToSeconds(instance.startAt) ?? instance.event.startAt)
      ? { startAt: isoToSeconds(instance.startAt) ?? instance.event.startAt }
      : {}),
    ...((isoToSeconds(instance.endAt) ?? instance.event.endAt)
      ? { endAt: isoToSeconds(instance.endAt) ?? instance.event.endAt }
      : {}),
  }));
}

/** Local-component Date for a `YYYY-MM-DD` key, or null when it is not one. */
function dateFromKey(key: string): Date | null {
  const [y, m, d] = key.split('-').map(Number);
  if (!y || !m || !d) return null;
  return new Date(y, m - 1, d);
}

/** Whole-day difference between two local `YYYY-MM-DD` keys (`to - from`). */
function dayDeltaBetweenKeys(fromKey: string, toKey: string): number {
  const [fy, fm, fd] = fromKey.split('-').map(Number);
  const [ty, tm, td] = toKey.split('-').map(Number);
  if (!fy || !fm || !fd || !ty || !tm || !td) return 0;
  const from = new Date(fy, fm - 1, fd).getTime();
  const to = new Date(ty, tm - 1, td).getTime();
  return Math.round((to - from) / 86_400_000);
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
   * The day the mobile view has open, as `YYYY-MM-DD`, or null.
   *
   * Only the phone view opens one: its chips are too small to press, so
   * the day sheet is where an event or a task is opened and where a new
   * event is created. The desktop grid operates on its cells directly
   * and never sets this.
   */
  const [openDayKey, setOpenDayKey] = useState<string | null>(null);
  // Bumped by the "Today" button so the mobile month-scroll re-centers.
  const [scrollToTodaySignal, setScrollToTodaySignal] = useState(0);

  // Close the mobile rail drawer when growing past the breakpoint
  // (the Drawer primitive owns Escape / focus-trap while it is open).
  useEffect(() => {
    if (!isMobile) setRailOpen(false);
  }, [isMobile]);

  // Same for the day sheet: grown past the breakpoint the grid behind it
  // is the desktop one, which the sheet has no place over.
  useEffect(() => {
    if (!isMobile) setOpenDayKey(null);
  }, [isMobile]);

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
  const weekStart: WeekStart = me.weekStart ?? 'mon';
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
      const shifted = shiftEventDays(source, delta);
      if (!shifted) return;
      const body: PatchEventInput = { startAt: shifted.startAt, endAt: shifted.endAt };
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
   * dateKey → calendar events (after layer filtering by kind).
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
    for (const ev of expandedEvents) {
      if (!ev.startAt) continue;
      // Each kind gates on its own layer flag; unknown kinds fall through
      // to the generic `events` flag so the UI never silently hides them.
      const k = ev.kind;
      if (k === 'block' && !layers.blocks) continue;
      if (k === 'free' && !layers.free) continue;
      if (k === 'milestone' && !layers.milestone) continue;
      if (k !== 'block' && k !== 'free' && k !== 'milestone' && !layers.events) continue;
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
  }, [expandedEvents, layers.events, layers.blocks, layers.free, layers.milestone, zone]);

  /**
   * Flat layer-filtered event list for the mobile month-scroll. Mirrors
   * the kind-gating in {@link eventsByDate} but keeps multi-day events
   * whole so the week-row layout can stretch them across columns (the
   * day-keyed map above would otherwise drop their span).
   */
  const filteredEvents = useMemo(() => {
    return expandedEvents.filter((ev) => {
      if (typeof ev.startAt !== 'number') return false;
      const k = ev.kind;
      if (k === 'block') return layers.blocks;
      if (k === 'free') return layers.free;
      if (k === 'milestone') return layers.milestone;
      return layers.events;
    });
  }, [expandedEvents, layers.events, layers.blocks, layers.free, layers.milestone]);

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

  const dragPayload = pointerDrag.drag?.payload ?? null;
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

      {/* Mobile-only trigger to open the calendars rail as a drawer. */}
      {isMobile ? (
        <div className={styles.mobileRailBar}>
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

      <div className={calendarLayoutStyles.layout}>
        {isMobile ? null : <CalendarsRail workspaces={railWorkspaces} selfUserId={selfUserId} />}
        <div className={styles.gridColumn}>
          {/* Above the grid rather than in a toast: the window is
              refetched on every month the scroll crosses, so a dead
              network would raise one toast per crossing. It goes away
              on its own the moment a refetch returns. */}
          {loadErrorMessage !== null ? (
            <div className={styles.loadError} role="status">
              <p className={styles.loadError__message}>{loadErrorMessage}</p>
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
              <div className={styles.grid}>
                {cells.map((cell) => {
                  const dayTasks = byDate.get(cell.key) ?? [];
                  const dayEvents = eventsByDate.get(cell.key) ?? [];
                  const dayHolidays = holidaysByDate.get(cell.key) ?? [];
                  const totalCount = dayTasks.length + dayEvents.length;
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
                    <div
                      key={cell.key}
                      // Registers the cell as a drop target: the gesture
                      // hit-tests these rects rather than asking the DOM
                      // what is under the pointer, which a finger — whose
                      // events stay aimed at the element it started on —
                      // would answer wrongly.
                      ref={pointerDrag.dropCellRef(cell.key)}
                      // data-cell-key exposes the YYYY-MM-DD cell address for
                      // E2E drag-target selection (otherwise CSS-module hashes
                      // make cells un-addressable).
                      data-cell-key={cell.key}
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
                        ) : totalCount > 0 ? (
                          <Badge tone="neutral" className={styles.countBadge}>
                            {totalCount}
                          </Badge>
                        ) : null}
                      </div>
                      <ul className={styles.pillList}>
                        {dayTasks.slice(0, 3).map((task) => {
                          const sourceKey = `task-${task.id}@${cell.key}`;
                          const dotColor =
                            STATE_COLOR[task.derivedState as TaskDerivedState] ??
                            'var(--nf-color-fg-muted)';
                          return (
                            <li key={task.id}>
                              <Link
                                to="/tasks/$taskId"
                                params={{ taskId: task.id }}
                                title={`${task.title} · ${task.workspaceName}`}
                                // The browser drags a link's URL of its own
                                // accord, which would run alongside this
                                // gesture and drop a hyperlink somewhere.
                                draggable={false}
                                onPointerDown={(e) => {
                                  pointerDrag.pressSource(e, sourceKey, {
                                    type: 'task',
                                    taskId: task.id,
                                    fromDate: cell.key,
                                    label: task.title,
                                    dotColor,
                                  });
                                }}
                                className={cx(
                                  styles.taskPill,
                                  pointerDrag.holdingKey === sourceKey && styles['pill--holding'],
                                  pointerDrag.drag?.sourceKey === sourceKey &&
                                    styles['pill--lifted'],
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
                                <span
                                  aria-hidden
                                  className={styles.taskPill__dot}
                                  style={{ background: dotColor }}
                                />
                                <span className={styles.taskPill__title}>{task.title}</span>
                              </Link>
                            </li>
                          );
                        })}
                        {dayTasks.length > 3 ? (
                          <li className={styles.morePill}>
                            {t('calendar.more', { count: dayTasks.length - 3 })}
                          </li>
                        ) : null}
                        {dayEvents.slice(0, 2).map((ev) => {
                          const pill = pillStyleForKind(ev.kind);
                          const sourceKey = `ev-${ev.id}@${cell.key}`;
                          return (
                            <li key={`ev-${ev.id}`}>
                              <button
                                type="button"
                                title={`${ev.title} · ${ev.workspaceName}`}
                                aria-label={t('calendar.event_detail.open_label', {
                                  title: ev.title,
                                  workspace: ev.workspaceName,
                                })}
                                onPointerDown={(e) => {
                                  pointerDrag.pressSource(e, sourceKey, {
                                    type: 'event',
                                    event: ev,
                                    fromDate: cell.key,
                                    label: ev.title,
                                    dotColor: pill.markerColor,
                                  });
                                }}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  // A drag ends in a click on the pill it
                                  // started from; opening the dialog then
                                  // would bury the move under a form.
                                  if (pointerDrag.wasDragged()) return;
                                  handleEventOpen(ev);
                                }}
                                className={cx(
                                  styles.eventPill,
                                  ev.viewerAttending && styles['eventPill--viewerAttending'],
                                  styles['eventPill--draggable'],
                                  pointerDrag.holdingKey === sourceKey && styles['pill--holding'],
                                  pointerDrag.drag?.sourceKey === sourceKey &&
                                    styles['pill--lifted'],
                                )}
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
                                    role="img"
                                    className={styles.eventPill__attendees}
                                    aria-label={t('calendar.event_attendee_count', {
                                      count: ev.attendeeCount,
                                    })}
                                  >
                                    <Users
                                      aria-hidden
                                      className={styles.eventPill__attendeesIcon}
                                    />
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
            </>
          )}
        </div>

        {isMobile ? null : <PendingInvitesPanel />}
      </div>

      {/* Mobile calendars-rail drawer — reuses the Drawer primitive for
          focus trap, overlay lock, Escape, and backdrop a11y. */}
      {isMobile && railOpen ? (
        <Drawer
          open
          onClose={() => setRailOpen(false)}
          title={t('calendars_rail.title')}
          side="inline-end"
        >
          <CalendarsRail workspaces={railWorkspaces} selfUserId={selfUserId} />
        </Drawer>
      ) : null}

      {/* The day the phone view has open. Each handler closes it first:
          what it opens is a dialog or another route, and a sheet left
          standing behind either is a surface nobody asked to keep. */}
      {isMobile && openDayKey !== null ? (
        <DayDetailSheet
          dateKey={openDayKey}
          locale={locale}
          zone={zone}
          events={eventsByDate.get(openDayKey) ?? EMPTY_EVENTS}
          tasks={byDate.get(openDayKey) ?? EMPTY_TASKS}
          holidays={holidaysByDate.get(openDayKey) ?? EMPTY_HOLIDAYS}
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

      {editTarget !== null ? (
        <EventDialog
          open
          zone={zone}
          workspaceId={
            editTarget.mode === 'edit' ? editTarget.event.workspaceId : (activeWsId ?? '')
          }
          projects={allProjects}
          mode={toDialogMode(editTarget)}
          onClose={() => setEditTarget(null)}
          onSaved={handleSaved}
        />
      ) : null}

      {/* A repeating event dropped on another day asks the same question
          the edit dialog asks before it saves. Cancelling sends nothing:
          the pill never left its day, because nothing here moves until
          the server has agreed. */}
      {pendingMove !== null ? (
        <RecurringScopeDialog
          open
          action="save"
          pending={updateEventMut.isPending}
          onCancel={() => setPendingMove(null)}
          onConfirm={(scope) => {
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
      {dragPayload !== null
        ? createPortal(
            <div ref={pointerDrag.proxyRef} className={styles.dragProxy} aria-hidden>
              <span
                className={cx(
                  styles.dragProxy__marker,
                  // A task is marked by a dot and an event by a diamond
                  // on the grid; the copy keeps whichever it came from,
                  // so what is moving stays recognisable.
                  dragPayload.type === 'task' && styles['dragProxy__marker--dot'],
                )}
                style={{ background: dragPayload.dotColor }}
              />
              <span className={styles.dragProxy__title}>{dragPayload.label}</span>
              {dragTargetLabel !== null ? (
                <span className={styles.dragProxy__target}>{dragTargetLabel}</span>
              ) : null}
            </div>,
            document.body,
          )
        : null}
      <p aria-live="polite" className={styles.srOnly}>
        {dragTargetLabel ?? ''}
      </p>
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
    // Omitted rather than nulled for a row that does not repeat: the
    // dialog reads the member's absence as "there is no occurrence to
    // scope a write to", and offers no extra step.
    ...(target.occurrenceStart === null
      ? {}
      : { occurrence: { originalStartAt: target.occurrenceStart } }),
  };
}

export const Route = createFileRoute('/_authenticated/calendar')({
  component: CalendarRoute,
});
