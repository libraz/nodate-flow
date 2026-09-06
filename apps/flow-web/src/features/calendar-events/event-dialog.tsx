/**
 * EventDialog — unified create / edit dialog for every calendar item kind
 * (task, event, block, free, milestone).
 *
 * The header + title + time row stay anchored as the `kind` segmented
 * control switches; only the kind-specific section morphs in with a
 * 180ms fade (respecting `prefers-reduced-motion`). Form state is kept
 * in local React state — no form library — because the morphing
 * transitions between schemas with overlapping but not identical fields
 * are easier to reason about imperatively than through a resolver.
 *
 * Edit mode only supports calendar events (event / block / free /
 * milestone). Editing a task happens on the task detail route; the
 * kind picker disables "task" while editing.
 */

import type { components } from '@nodate-flow/sdk';
import type { RecurrenceRule } from '@nodate-flow/ui/calendar/types';
import Button from '@nodate-flow/ui/primitives/button';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import Select from '@nodate-flow/ui/primitives/select';
import Switch from '@nodate-flow/ui/primitives/switch';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import TimePicker from '@nodate-flow/ui/primitives/time-picker';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import type { Zone } from '@nodate-flow/ui/time';
import {
  type FormEvent,
  type KeyboardEvent,
  type ReactElement,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { allDayToUnix, todayKey, unixToWallClock, wallClockToUnix } from '../../lib/date-utils';
import { formatDate } from '../../lib/format';
import { useWeekStart } from '../../lib/use-week-start';
import { selectUser, useAuth } from '../auth/auth-store';
import { TASK_PRIORITIES, type TaskPriority } from '../tasks/api';
import { PRIORITY_KEY } from '../tasks/constants';
import {
  type CreateEventInput,
  type PatchEventInput,
  type RecurrenceScope,
  rememberCalendarChoice,
  useCalendarsQuery,
  useCreateCalendarTask,
  useCreateEvent,
  useDefaultCalendarId,
  useDeleteEvent,
  useEventDetailQuery,
  useUpdateEvent,
} from './api';
import AttendeesSection from './attendees-section';
import CreatorChip from './creator-chip';
import styles from './event-dialog.module.css';
import RecurringScopeDialog from './recurring-scope-dialog';

type FlowProject = components['schemas']['Project'];
type CalEventLike = components['schemas']['MyCalendarEventResponse'];

/** Every item kind selectable in the dialog. */
export type ItemKind = 'task' | 'event' | 'block' | 'free' | 'milestone';

/** Subset of {@link ItemKind} that maps to a calendar event row. */
export type CalEventKind = Exclude<ItemKind, 'task'>;

export type ShowAs = 'busy' | 'free' | 'tentative' | 'oof';

/**
 * Whether the commitment can be moved. Deliberately a separate control
 * from {@link ShowAs}: `showAs` is what free/busy consumers read, so
 * saying "tentative" to mean "I would move this" misreports the time as
 * not really taken to everyone outside this app.
 */
export type Flexibility = 'fixed' | 'negotiable' | 'conditional';

/** Block preset chip identifiers. Maps to `blockLabel` + `showAs` on submit. */
export type BlockPreset = 'working' | 'focus' | 'oof' | 'custom';

/** Recurrence preset identifiers stored in component state. */
/**
 * The repeat choices the dialog offers, plus `custom` — which is not
 * offered, only shown.
 *
 * The presets are lossy: a stored rule can say things none of them can
 * (an interval, a set of weekdays that is not the working week). Before,
 * that was handled by not showing the stored rule at all, so every event
 * opened reading "Does not repeat" whether or not it repeated, and the
 * only way to stop a daily standup was to delete the series. `custom`
 * lets the control tell the truth about a rule it cannot offer to
 * recreate, while still allowing the user to replace it or clear it.
 */
export type RecurrencePreset =
  | 'none'
  | 'daily'
  | 'weekdays'
  | 'weekly'
  | 'monthly'
  | 'yearly'
  | 'custom';

/** Notification preset identifiers. `none` omits the field on submit. */
export type NotificationPreset =
  | 'none'
  | 'at_time'
  | '5min'
  | '10min'
  | '15min'
  | '30min'
  | '1hour'
  | '1day';

/**
 * The rendered occurrence an edit dialog was opened from, for a row that
 * repeats.
 *
 * `originalStartAt` is the instant the series rule produced for that
 * occurrence — not the start the user is about to type. The two are the
 * same number type and would be interchangeable if this were a bare
 * `occurrenceStart: number` beside the event, and sending the edited one
 * writes an override against an occurrence nobody opened. Naming it
 * `originalStartAt` inside its own object keeps the form's `startDate` /
 * `startTime` state and this value from ever standing in for each other,
 * and makes the field impossible to populate except from the grid, which
 * is the only place that knows which instance was clicked.
 */
export interface DialogOccurrence {
  originalStartAt: number;
}

export type EventDialogMode =
  | { kind: 'create'; date: string; initialItemKind?: ItemKind }
  | {
      kind: 'edit';
      eventId: string;
      calendarId: string;
      initialKind: CalEventKind;
      event: CalEventLike;
      /** Absent when the dialog was not opened from an occurrence of a series. */
      occurrence?: DialogOccurrence;
    };

/**
 * A commit that is waiting on the "which occurrences?" answer. The
 * occurrence start is captured here when the question is raised, so the
 * request that eventually goes out reads it from the prompt rather than
 * from anything the form can still change.
 */
type ScopePrompt =
  | { action: 'save'; body: PatchEventInput; occurrenceStart: number }
  | { action: 'delete'; occurrenceStart: number };

export interface EventDialogProps {
  open: boolean;
  workspaceId: string;
  /**
   * Effective zone (profile, else workspace, else browser). Both stamped
   * on events this dialog creates and used to read every wall clock it
   * shows or submits.
   *
   * It used to be the browser's, which meant the profile setting had no
   * effect on the events it produced: a Tokyo user working in Berlin
   * created meetings labelled Europe/Berlin, and the reminders — which
   * already honoured the profile — disagreed about what time they were.
   * Then the label was corrected without the arithmetic, which was worse:
   * the request said Asia/Tokyo while the instant beside it had been
   * resolved in Europe/Berlin, so the stored event contradicted its own
   * declared zone. A `Zone` rather than a string because a string is as
   * easy to leave off as to pass.
   */
  zone: Zone;
  /** Projects available to the Task kind picker. */
  projects: FlowProject[];
  mode: EventDialogMode;
  onClose: () => void;
  onSaved: () => void;
}

/* ── helpers ────────────────────────────────────────────────────── */

/** Translate a notification preset to minutes (null = omit from payload). */
function presetToMinutes(preset: NotificationPreset): number | null {
  switch (preset) {
    case 'none':
      return null;
    case 'at_time':
      return 0;
    case '5min':
      return 5;
    case '10min':
      return 10;
    case '15min':
      return 15;
    case '30min':
      return 30;
    case '1hour':
      return 60;
    case '1day':
      return 24 * 60;
  }
}

/**
 * Translate a recurrence preset to a recurrence-rule payload. `'none'`
 * returns null so the caller omits the field.
 *
 * The emitted shape matches the canonical {@link RecurrenceRule} contract
 * shared with the recurrence expander (`@nodate-flow/ui/calendar`): `freq`
 * tokens are lowercase (`daily` / `weekly` / `monthly` / `yearly`) and
 * `byDay` weekday tokens are the lowercase two-letter forms the expander's
 * day map keys on (`mo` / `tu` / …). These are API enum values — not UI
 * copy — so they stay literal lowercase. The `@api` validator accepts the
 * same lowercase canonical set.
 */
export function presetToRRule(preset: RecurrencePreset, _startDate: string): RecurrenceRule | null {
  switch (preset) {
    case 'none':
      return null;
    // `custom` describes a rule this control did not author and cannot
    // reproduce, so selecting it changes nothing: null here means the
    // caller leaves the stored rule alone.
    case 'custom':
      return null;
    case 'daily':
      return { freq: 'daily' };
    case 'weekdays':
      return { freq: 'weekly', byDay: ['mo', 'tu', 'we', 'th', 'fr'] };
    case 'weekly':
      return { freq: 'weekly' };
    case 'monthly':
      return { freq: 'monthly' };
    case 'yearly':
      return { freq: 'yearly' };
  }
}

/**
 * Map a stored rule back onto the control, or `custom` when no preset
 * reproduces it.
 *
 * Deliberately strict: a rule with an interval, a count, an until or a
 * byMonthDay is not "weekly" even if its freq says so, because choosing
 * `weekly` in the control would silently drop the rest of it. Reporting
 * `custom` and leaving the rule untouched is the honest answer.
 */
export function rruleToPreset(rule: RecurrenceRule | null | undefined): RecurrencePreset {
  if (!rule) return 'none';
  const plain =
    (rule.interval === undefined || rule.interval === 1) &&
    rule.count === undefined &&
    rule.until === undefined &&
    (rule.byMonthDay === undefined || rule.byMonthDay.length === 0);
  if (!plain) return 'custom';

  const byDay = rule.byDay ?? [];
  switch (rule.freq) {
    case 'daily':
      return byDay.length === 0 ? 'daily' : 'custom';
    case 'weekly': {
      if (byDay.length === 0) return 'weekly';
      const weekdays = ['mo', 'tu', 'we', 'th', 'fr'];
      const normalized = [...byDay].map((d) => d.toLowerCase()).sort();
      return normalized.join(',') === [...weekdays].sort().join(',') ? 'weekdays' : 'custom';
    }
    case 'monthly':
      return byDay.length === 0 ? 'monthly' : 'custom';
    case 'yearly':
      return byDay.length === 0 ? 'yearly' : 'custom';
    default:
      return 'custom';
  }
}

/**
 * The instants an edit form should open on.
 *
 * A series is stored once, as its first occurrence, while the grid draws
 * one pill per instance the rule produces. Seeded from the stored row,
 * every instance therefore opens on the series' own dates: click the
 * meeting on 14 October and the fields read 16 September, and saving
 * that range is how an occurrence gets moved back onto the master's day.
 * The occurrence carries the instant the rule gave it, so the form opens
 * there and keeps the stored duration — the one part of the range the
 * occurrence does not itself state.
 *
 * The shift is arithmetic on instants rather than on wall clocks so
 * all-day and timed rows stay on a single path: an all-day span is a
 * whole number of days in seconds, and moving both ends by the same
 * amount preserves it either way.
 *
 * `occurrenceStartAt` is only ever {@link DialogOccurrence.originalStartAt}
 * — the start the rule produced, never the start the form is holding.
 * Passing the latter would shift the range onto itself and call the
 * result an occurrence.
 */
export function occurrenceRange(
  stored: { startAt?: number | null; endAt?: number | null },
  occurrenceStartAt: number | null,
): { startAt: number | null; endAt: number | null } {
  const startAt = stored.startAt ?? null;
  const endAt = stored.endAt ?? null;
  if (occurrenceStartAt === null || startAt === null) return { startAt, endAt };
  return {
    startAt: occurrenceStartAt,
    endAt: endAt === null ? null : occurrenceStartAt + (endAt - startAt),
  };
}

/**
 * The weekday a `YYYY-MM-DD` key falls on, named in `locale`.
 *
 * The weekly repeat option names the day it repeats on, and that day is
 * a property of the event's own start rather than a separate choice.
 * `Intl` answers it in whichever language is active, so there is no day
 * table to keep in step across the locale files and no key per weekday.
 * Read in UTC because the key is already a calendar day: giving it any
 * other zone lets an offset name the day before.
 */
export function weekdayName(dayKey: string, locale: string): string {
  const [year, month, day] = dayKey.split('-').map(Number);
  if (!year || !month || !day) return '';
  return new Intl.DateTimeFormat(locale, { weekday: 'long', timeZone: 'UTC' }).format(
    new Date(Date.UTC(year, month - 1, day)),
  );
}

/** Default start/end times for each kind, given the clicked date. */
function defaultTimes(kind: CalEventKind): { start: string; end: string } {
  switch (kind) {
    case 'event':
    case 'free':
      return { start: '09:00', end: '10:00' };
    case 'block':
      return { start: '09:00', end: '18:00' };
    case 'milestone':
      return { start: '00:00', end: '00:00' };
  }
}

/**
 * The wall-clock times the dialog opens with: the ones the range already
 * has when editing, the kind's defaults when creating.
 */
function initialTimesFor(
  modeKind: EventDialogMode['kind'],
  range: { startAt: number | null; endAt: number | null },
  initialKind: ItemKind,
  zone: Zone,
): { start: string; end: string } {
  if (modeKind === 'edit') {
    return {
      start: range.startAt != null ? unixToWallClock(range.startAt, zone).time : '09:00',
      end: range.endAt != null ? unixToWallClock(range.endAt, zone).time : '10:00',
    };
  }
  const kind: CalEventKind = initialKind === 'task' ? 'event' : initialKind;
  return defaultTimes(kind);
}

/**
 * True when the value the API returned describes a repeat rule.
 *
 * `recurrenceRule` is typed `unknown` on both event shapes (it is free
 * JSON on the wire), so presence is all the client can honestly read.
 */
function hasRecurrenceRule(value: unknown): boolean {
  if (value == null) return false;
  if (typeof value === 'string') return value.trim().length > 0;
  return typeof value === 'object';
}

/**
 * Whether a patch body rewrites the series definition itself.
 *
 * The API refuses a per-occurrence scope on such a patch, and rightly:
 * changing the rule is a statement about the whole series, so there is
 * nothing to ask. Those edits go out unscoped, exactly as before.
 */
function touchesRecurrence(body: PatchEventInput): boolean {
  if ('recurrenceRule' in body || 'recurrenceEnd' in body || 'recurrenceExceptions' in body) {
    return true;
  }
  return (body.clear ?? []).some(
    (field) =>
      field === 'recurrenceRule' || field === 'recurrenceEnd' || field === 'recurrenceExceptions',
  );
}

function blockPresetFromLabel(label?: string): BlockPreset {
  switch (label) {
    case 'working':
      return 'working';
    case 'focus':
      return 'focus';
    case 'oof':
      return 'oof';
    case undefined:
    case '':
      return 'working';
    default:
      return 'custom';
  }
}

/* ── static i18n key maps ──────────────────────────────────────── */

/**
 * Static lookup tables for translation keys that previously used dynamic
 * template-string interpolation. Keeping every literal key reachable
 * lets `i18next-parser` extract them and our locale-sync checker catch
 * misses across en/ja/zh.
 */

const DIALOG_TITLE_KEYS = {
  create: {
    task: 'dialog.title.create.task',
    event: 'dialog.title.create.event',
    block: 'dialog.title.create.block',
    free: 'dialog.title.create.free',
    milestone: 'dialog.title.create.milestone',
  },
  edit: {
    task: 'dialog.title.edit.task',
    event: 'dialog.title.edit.event',
    block: 'dialog.title.edit.block',
    free: 'dialog.title.edit.free',
    milestone: 'dialog.title.edit.milestone',
  },
} as const;

const KIND_LABEL_KEYS = {
  task: 'kind.task',
  event: 'kind.event',
  block: 'kind.block',
  free: 'kind.free',
  milestone: 'kind.milestone',
} as const;

const PLACEHOLDER_TITLE_KEYS = {
  task: 'placeholder.title.task',
  event: 'placeholder.title.event',
  block: 'placeholder.title.block',
  free: 'placeholder.title.free',
  milestone: 'placeholder.title.milestone',
} as const;

const TOAST_CREATED_KEYS = {
  task: 'toast.created.task',
  event: 'toast.created.event',
  block: 'toast.created.block',
  free: 'toast.created.free',
  milestone: 'toast.created.milestone',
} as const;

const TOAST_UPDATED_KEYS = {
  task: 'toast.updated.task',
  event: 'toast.updated.event',
  block: 'toast.updated.block',
  free: 'toast.updated.free',
  milestone: 'toast.updated.milestone',
} as const;

const TOAST_DELETED_KEYS = {
  task: 'toast.deleted.task',
  event: 'toast.deleted.event',
  block: 'toast.deleted.block',
  free: 'toast.deleted.free',
  milestone: 'toast.deleted.milestone',
} as const;

const SHOW_AS_KEYS = {
  busy: 'showAs.busy',
  free: 'showAs.free',
  tentative: 'showAs.tentative',
  oof: 'showAs.oof',
} as const satisfies Record<ShowAs, string>;

const FLEXIBILITY_KEYS = {
  fixed: 'flexibility.fixed',
  negotiable: 'flexibility.negotiable',
  conditional: 'flexibility.conditional',
} as const satisfies Record<Flexibility, string>;

const BLOCK_PRESET_KEYS = {
  working: 'blockLabel.preset.working',
  focus: 'blockLabel.preset.focus',
  oof: 'blockLabel.preset.oof',
  custom: 'blockLabel.preset.custom',
} as const satisfies Record<BlockPreset, string>;

const RECURRENCE_PRESET_KEYS = {
  none: 'recurrence.preset.none',
  daily: 'recurrence.preset.daily',
  weekdays: 'recurrence.preset.weekdays',
  weekly: 'recurrence.preset.weekly',
  monthly: 'recurrence.preset.monthly',
  yearly: 'recurrence.preset.yearly',
  custom: 'recurrence.preset.custom',
} as const satisfies Record<RecurrencePreset, string>;

const NOTIFICATION_PRESET_KEYS = {
  none: 'notification.preset.none',
  // biome-ignore lint/style/useNamingConvention: matches NotificationPreset literal union
  at_time: 'notification.preset.at_time',
  '5min': 'notification.preset.5min',
  '10min': 'notification.preset.10min',
  '15min': 'notification.preset.15min',
  '30min': 'notification.preset.30min',
  '1hour': 'notification.preset.1hour',
  '1day': 'notification.preset.1day',
} as const satisfies Record<NotificationPreset, string>;

/* ── dirty tracking ─────────────────────────────────────────────── */

/**
 * Every form control the dialog tracks individually.
 *
 * PATCH treats a present field as "set this value", so an edit payload
 * built from the whole form writes back fields the user never opened —
 * silently replacing stored content with whatever the dialog happened to
 * be seeded with. Recording which controls the user actually moved lets
 * {@link EventDialog} send exactly those.
 */
type TrackedField =
  | 'kind'
  | 'title'
  | 'startDate'
  | 'endDate'
  | 'startTime'
  | 'endTime'
  | 'allDay'
  | 'calendarId'
  | 'projectId'
  | 'priority'
  | 'showAs'
  | 'flexibility'
  | 'location'
  | 'blockPreset'
  | 'blockCustomLabel'
  | 'recurrence'
  | 'notification'
  | 'memo';

/**
 * Time controls are patched as one unit: the API rejects a start without
 * a matching end, and flipping all-day changes what the pair means. Any
 * one of them moving re-sends the whole range.
 */
const TIME_FIELDS: readonly TrackedField[] = [
  'startDate',
  'endDate',
  'startTime',
  'endTime',
  'allDay',
];

/* ── component ──────────────────────────────────────────────────── */

const KIND_OPTIONS: ItemKind[] = ['task', 'event', 'block', 'free', 'milestone'];

export default function EventDialog({
  open,
  workspaceId,
  zone,
  projects,
  mode,
  onClose,
  onSaved,
}: EventDialogProps): ReactElement | null {
  const { t, i18n } = useTranslation('calendar-events');
  const weekStart = useWeekStart();
  const { t: tCommon } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  // `returnObjects` falls back to the raw key string when no resource
  // bundle is registered (e.g. unit tests). Coerce to an empty array so
  // DatePicker's map call never crashes in that case — DatePicker handles
  // empty labels gracefully.
  const weekdayLabelsRaw = tCommon('common.date.weekdays', { returnObjects: true });
  const weekdayLabels: string[] = Array.isArray(weekdayLabelsRaw) ? weekdayLabelsRaw : [];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });

  /* ── initial derivation ─── */

  const isCreate = mode.kind === 'create';
  const isEdit = mode.kind === 'edit';

  // Current actor — used by the AttendeesSection to detect "self" rows
  // and gate owner-only controls. Falls back to an empty string when the
  // user is somehow not yet bootstrapped (treated as "no privileges").
  const currentUser = useAuth(selectUser);
  const selfUserId = currentUser?.id ?? '';

  const initialKind: ItemKind =
    mode.kind === 'create' ? (mode.initialItemKind ?? 'event') : mode.initialKind;

  /**
   * The occurrence the dialog was opened on, as a plain instant.
   *
   * Kept as a number rather than as the {@link DialogOccurrence} the mode
   * carries because the hydration effect below depends on it: the route
   * builds that object afresh on every render, so an effect keyed on it
   * would re-run continuously.
   */
  const openedOccurrenceStart: number | null =
    mode.kind === 'edit' && mode.occurrence !== undefined ? mode.occurrence.originalStartAt : null;

  // An edit opens on the occurrence that was clicked when there is one;
  // a row that does not repeat has only its own range to show.
  const initialRange =
    mode.kind === 'edit'
      ? occurrenceRange(mode.event, openedOccurrenceStart)
      : { startAt: null, endAt: null };

  const initialDate =
    mode.kind === 'create' ? mode.date : inferEditDate(initialRange.startAt, zone);

  /* ── dirty tracking ─── */

  // A ref, not state: nothing renders from it, and a control marking
  // itself dirty must not cost a render pass on every keystroke.
  const dirtyFieldsRef = useRef<Set<TrackedField>>(new Set());

  function markDirty(field: TrackedField): void {
    dirtyFieldsRef.current.add(field);
  }

  /** Whether the user moved any of the given controls. */
  function anyDirty(...fields: TrackedField[]): boolean {
    return fields.some((f) => dirtyFieldsRef.current.has(f));
  }

  /** Wrap a state setter so moving its control records the field as edited. */
  function editing<T>(field: TrackedField, apply: (value: T) => void): (value: T) => void {
    return (value: T) => {
      markDirty(field);
      apply(value);
    };
  }

  /* ── form state ─── */

  const [kind, setKind] = useState<ItemKind>(initialKind);
  const [title, setTitle] = useState<string>(mode.kind === 'edit' ? mode.event.title : '');

  // Time/date fields. Tasks use `startOn`/`dueOn`; event kinds use
  // startDate/startTime + endDate/endTime + allDay.
  const [startDate, setStartDate] = useState<string>(initialDate);
  const [endDate, setEndDate] = useState<string>(
    initialRange.endAt != null ? unixToWallClock(initialRange.endAt, zone).date : initialDate,
  );

  // Read only on the first render — as the initial value of the two time
  // controls and of `lastTimeRef` — so it is a plain derivation rather
  // than a memo.
  const initialTimes = initialTimesFor(mode.kind, initialRange, initialKind, zone);

  const [startTime, setStartTime] = useState<string>(initialTimes.start);
  const [endTime, setEndTime] = useState<string>(initialTimes.end);
  const lastTimeRef = useRef<{ start: string; end: string }>(initialTimes);

  const [allDay, setAllDay] = useState<boolean>(
    mode.kind === 'edit' ? (mode.event.allDay ?? false) : false,
  );

  // Calendar binding (non-task kinds).
  const calendarsQuery = useCalendarsQuery(workspaceId);
  const defaultCalId = useDefaultCalendarId(workspaceId);
  const [calendarId, setCalendarId] = useState<string>(mode.kind === 'edit' ? mode.calendarId : '');
  useEffect(() => {
    if (calendarId) return;
    if (defaultCalId) setCalendarId(defaultCalId);
  }, [calendarId, defaultCalId]);

  // Task-only.
  const [projectId, setProjectId] = useState<string>(projects[0]?.id ?? '');
  const [priority, setPriority] = useState<TaskPriority>(2);

  // Event-only.
  const [showAs, setShowAs] = useState<ShowAs>(
    mode.kind === 'edit' ? ((mode.event.showAs as ShowAs) ?? 'busy') : 'busy',
  );
  // A missing value reads as 'fixed', matching the column default: an
  // event whose owner never said it could move must not advertise time
  // they did not offer.
  const [flexibility, setFlexibility] = useState<Flexibility>(
    mode.kind === 'edit' ? ((mode.event.flexibility as Flexibility) ?? 'fixed') : 'fixed',
  );
  const [location, setLocation] = useState<string>(
    mode.kind === 'edit' ? (mode.event.location ?? '') : '',
  );

  // Block-only.
  const [blockPreset, setBlockPreset] = useState<BlockPreset>(
    mode.kind === 'edit' && mode.initialKind === 'block'
      ? blockPresetFromLabel(mode.event.blockLabel)
      : 'working',
  );
  const [blockCustomLabel, setBlockCustomLabel] = useState<string>(
    mode.kind === 'edit' && mode.initialKind === 'block' && mode.event.blockLabel
      ? mode.event.blockLabel
      : '',
  );

  // More options (recurrence, notification, memo/description).
  const [expanded, setExpanded] = useState<boolean>(false);
  const [recurrence, setRecurrence] = useState<RecurrencePreset>('none');
  const [notification, setNotification] = useState<NotificationPreset>('none');
  // MyCalendarEventResponse (the `/me/calendar-events` aggregate shape used by
  // the calendar grid) omits `memo`; it arrives with the event detail below.
  const [memo, setMemo] = useState<string>('');

  /* ── hydrate the edit form from the authoritative event row ─── */

  const detailQuery = useEventDetailQuery(
    workspaceId,
    mode.kind === 'edit' ? mode.calendarId : '',
    mode.kind === 'edit' ? mode.eventId : '',
    open && mode.kind === 'edit',
  );
  const detail = detailQuery.data;
  // True only while the dialog has an event whose full body is still in
  // flight — create mode never fetches, so its query stays pending forever.
  const detailLoading = isEdit && detailQuery.isLoading;

  // One-shot: the response is the starting point, not a live binding.
  // Controls the user already moved keep their value so a slow response
  // never overwrites typing.
  const hydratedRef = useRef<boolean>(false);

  // Reopening the dialog starts a fresh edit session: drop the recorded
  // edits and hydrate again. Declared before the hydration effect so the
  // reset lands first on the render that reopens. Comparing against a
  // value snapshot would not do — hydration legitimately changes field
  // values without the user having touched anything, and that must not
  // read as an unsaved edit.
  useEffect(() => {
    if (!open) return;
    dirtyFieldsRef.current = new Set();
    hydratedRef.current = false;
  }, [open]);

  useEffect(() => {
    if (!open || !detail || hydratedRef.current) return;
    hydratedRef.current = true;
    const dirty = dirtyFieldsRef.current;

    if (!dirty.has('title')) setTitle(detail.title);
    if (!dirty.has('memo')) setMemo(detail.memo ?? '');
    if (!dirty.has('location')) setLocation(detail.location ?? '');
    if (!dirty.has('showAs')) setShowAs((detail.showAs as ShowAs) || 'busy');
    if (!dirty.has('flexibility')) setFlexibility((detail.flexibility as Flexibility) || 'fixed');
    if (!dirty.has('blockPreset')) setBlockPreset(blockPresetFromLabel(detail.blockLabel));
    if (!dirty.has('blockCustomLabel')) setBlockCustomLabel(detail.blockLabel ?? '');
    if (!TIME_FIELDS.some((f) => dirty.has(f))) {
      setAllDay(detail.allDay);
      // The authoritative row is the series' own range, so it lands on
      // the occurrence that was opened before it reaches the controls.
      // Seeding, never editing: these setters bypass `markDirty` on
      // purpose, because one marked time control re-sends the whole
      // range — a scoped save would then write the dialog's starting
      // values back over an occurrence the user only looked at.
      const range = occurrenceRange(detail, openedOccurrenceStart);
      if (range.startAt != null) {
        const wall = unixToWallClock(range.startAt, zone);
        setStartDate(wall.date);
        setStartTime(wall.time);
      }
      if (range.endAt != null) {
        const wall = unixToWallClock(range.endAt, zone);
        setEndDate(wall.date);
        setEndTime(wall.time);
      }
    }
    // A memo sits behind the "more options" disclosure, so an event that
    // has one opens with the panel already down — otherwise the only
    // long-form content on the event is invisible while the user edits.
    if ((detail.memo ?? '') !== '') setExpanded(true);

    // Seed the repeat control from the stored rule. Leaving it at
    // 'none' meant a recurring event opened claiming it did not repeat,
    // and the only way to stop one was to delete the whole series. A
    // rule no preset reproduces shows as 'custom', which still leaves it
    // untouched unless the user picks something else.
    setRecurrence(rruleToPreset(detail.recurrenceRule as RecurrenceRule | null | undefined));
    if (detail.recurrenceRule) setExpanded(true);
    // `zone` decides which wall clock the stored instants seed the form
    // with, so it belongs in the dependency list even though it does not
    // change while a dialog is open.
  }, [detail, open, zone, openedOccurrenceStart]);

  /* ── recurring-scope choice ─── */

  /**
   * The occurrence a scope choice would act on, or null when the dialog
   * must not ask.
   *
   * Null covers three cases that all mean the same thing: create mode,
   * an edit the grid opened from a row it did not expand from a rule,
   * and — once the authoritative row lands — a row the aggregate labelled
   * as repeating but which carries no rule. Deriving one nullable number
   * rather than a boolean beside it means every call site that acts on
   * the choice has to hold the value it acts with.
   */
  const scopeOccurrenceStart: number | null =
    mode.kind === 'edit' &&
    mode.occurrence !== undefined &&
    (detail === undefined || hasRecurrenceRule(detail.recurrenceRule))
      ? mode.occurrence.originalStartAt
      : null;

  const [scopePrompt, setScopePrompt] = useState<ScopePrompt | null>(null);

  // Validation state.
  const [titleError, setTitleError] = useState<string | null>(null);
  const [timeError, setTimeError] = useState<string | null>(null);
  const [calendarError, setCalendarError] = useState<string | null>(null);
  const [projectError, setProjectError] = useState<string | null>(null);

  /* ── morph effect: drop kind-specific fields when leaving a kind ─── */

  const prevKindRef = useRef<ItemKind>(kind);
  useEffect(() => {
    if (prevKindRef.current === kind) return;
    const prev = prevKindRef.current;
    prevKindRef.current = kind;

    // Leaving Block clears block-only fields.
    if (prev === 'block' && kind !== 'block') {
      setBlockPreset('working');
      setBlockCustomLabel('');
    }
    // Leaving Event clears event-only fields (location kept unhelpful elsewhere).
    if (prev === 'event' && kind !== 'event') {
      setLocation('');
    }
    // Milestone collapses to single date — align end to start so a
    // subsequent re-selection of Event starts from a sensible pair.
    if (kind === 'milestone') {
      setEndDate(startDate);
    }
    // Going to Free does not preserve event-only extras either.
    if (prev !== 'task' && kind === 'task') {
      // Task has no time-of-day; end date stays as the due date.
    }
    // Re-seed default times when entering a kind that previously had
    // different sensible defaults, but only if the user hasn't obviously
    // customised them (keep the heuristic simple: preserve times).
  }, [kind, startDate]);

  /* ── all-day toggle preserves last-used time ─── */

  const handleAllDayChange = (next: boolean): void => {
    markDirty('allDay');
    if (next) {
      lastTimeRef.current = { start: startTime, end: endTime };
    } else {
      setStartTime(lastTimeRef.current.start);
      setEndTime(lastTimeRef.current.end);
    }
    setAllDay(next);
  };

  /* ── mutations ─── */

  const createEvent = useCreateEvent();
  const updateEvent = useUpdateEvent();
  const deleteEvent = useDeleteEvent();
  const createTask = useCreateCalendarTask();

  const isPending =
    createEvent.isPending || updateEvent.isPending || deleteEvent.isPending || createTask.isPending;

  /* ── derived labels ─── */

  const headerTitle = t(DIALOG_TITLE_KEYS[isCreate ? 'create' : 'edit'][kind]);

  const kindOptions: SegmentedControlOption<ItemKind>[] = KIND_OPTIONS.map((k) => ({
    value: k,
    label: t(KIND_LABEL_KEYS[k]),
    tone: k,
    // Task kind is not a valid edit target (event→task morph has no
    // backend semantics). Lock the picker when editing.
    disabled: isEdit && k === 'task',
  }));

  const calendarOptions = useMemo(
    () =>
      (calendarsQuery.data ?? []).map((c) => ({
        value: c.id,
        label: c.name,
      })),
    [calendarsQuery.data],
  );

  /* ── submit ─── */

  function validate(): boolean {
    let ok = true;
    if (!title.trim()) {
      setTitleError(t('validation.titleRequired'));
      ok = false;
    } else {
      setTitleError(null);
    }
    if (kind !== 'task' && kind !== 'milestone') {
      // Time-ordered validation only where both ends are meaningful.
      if (!allDay) {
        const s = wallClockToUnix(startDate, startTime, zone);
        const e = wallClockToUnix(endDate, endTime, zone);
        if (e <= s) {
          setTimeError(t('validation.endBeforeStart'));
          ok = false;
        } else {
          setTimeError(null);
        }
      } else {
        setTimeError(null);
      }
    } else {
      setTimeError(null);
    }
    if (kind !== 'task' && !calendarId) {
      setCalendarError(t('validation.calendarRequired'));
      ok = false;
    } else {
      setCalendarError(null);
    }
    if (kind === 'task' && !projectId) {
      setProjectError(t('validation.projectRequired'));
      ok = false;
    } else {
      setProjectError(null);
    }
    return ok;
  }

  async function handleSubmit(ev: FormEvent<HTMLFormElement> | null): Promise<void> {
    if (ev) ev.preventDefault();
    if (isPending) return;
    if (!validate()) return;

    if (kind !== 'task' && mode.kind === 'edit') {
      const body = buildPatchBody();
      // Nothing was touched: skip the round-trip rather than replay the
      // stored values back at the server, which would append an update
      // event and an audit row for a no-op.
      if (Object.keys(body).length === 0) {
        toaster.show({ tone: 'success', message: t(TOAST_UPDATED_KEYS[kind]) });
        onSaved();
        return;
      }
      // A repeating row has to say which occurrences the edit reaches
      // before anything is written. An edit that rewrites the rule is a
      // series-level statement by construction, so it does not ask.
      if (scopeOccurrenceStart !== null && !touchesRecurrence(body)) {
        setScopePrompt({ action: 'save', body, occurrenceStart: scopeOccurrenceStart });
        return;
      }
      await commitPatch(body, null);
      return;
    }

    try {
      if (kind === 'task') {
        await createTask.mutateAsync({
          projectId,
          title: title.trim(),
          ...(memo.trim() ? { description: memo.trim() } : {}),
          ...(startDate ? { startOn: startDate } : {}),
          dueOn: endDate || startDate,
          priority,
          visibility: 'project',
        });
        toaster.show({ tone: 'success', message: t('toast.created.task') });
      } else {
        const body = buildCreateBody();
        await createEvent.mutateAsync({ workspaceId, calendarId, body });
        rememberCalendarChoice(workspaceId, calendarId);
        toaster.show({ tone: 'success', message: t(TOAST_CREATED_KEYS[kind]) });
      }
      onSaved();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, isCreate ? 'toast.createFailed' : 'toast.updateFailed'),
      });
    }
  }

  /**
   * Send the PATCH.
   *
   * `scoped` is present only once a repeating row's scope has been
   * answered, and it carries the occurrence start with it — the request
   * therefore reads the instant from the answered question rather than
   * from form state, which is what keeps an edited start from being
   * mistaken for the occurrence's own. `series` sends no occurrence at
   * all, since none identifies it.
   */
  async function commitPatch(
    body: PatchEventInput,
    scoped: { scope: RecurrenceScope; occurrenceStart: number } | null,
  ): Promise<void> {
    if (mode.kind !== 'edit') return;
    const payload: PatchEventInput =
      scoped === null
        ? body
        : scoped.scope === 'series'
          ? { ...body, scope: 'series' }
          : { ...body, scope: scoped.scope, occurrenceStart: scoped.occurrenceStart };
    try {
      await updateEvent.mutateAsync({
        workspaceId,
        calendarId,
        eventId: mode.eventId,
        body: payload,
      });
      rememberCalendarChoice(workspaceId, calendarId);
      setScopePrompt(null);
      toaster.show({ tone: 'success', message: t(TOAST_UPDATED_KEYS[kind]) });
      onSaved();
    } catch (err) {
      // Back to the edit form with the reason on screen — including the
      // typed refusals a scope can earn, which name a condition the user
      // can act on by choosing differently.
      setScopePrompt(null);
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'toast.updateFailed'),
      });
    }
  }

  function buildCreateBody(): CreateEventInput {
    const calKind = kind as CalEventKind;
    const body: CreateEventInput = {
      title: title.trim(),
      kind: calKind,
      // The declared zone and the instants below have to be resolved
      // from the same value, or the row contradicts itself.
      timezone: zone.name,
      allDay: calKind === 'milestone' ? true : allDay,
    };
    if (calKind === 'milestone') {
      // Backend enforces (StartAt == nil) != (EndAt == nil); set both.
      body.startAt = allDayToUnix(startDate);
      body.endAt = body.startAt;
    } else if (allDay) {
      body.startAt = allDayToUnix(startDate);
      body.endAt = allDayToUnix(endDate);
    } else {
      body.startAt = wallClockToUnix(startDate, startTime, zone);
      body.endAt = wallClockToUnix(endDate, endTime, zone);
    }
    if (calKind === 'event') {
      body.showAs = showAs;
      body.flexibility = flexibility;
      if (location.trim()) body.location = location.trim();
    }
    if (calKind === 'block') {
      const label = blockPreset === 'custom' ? blockCustomLabel.trim() : blockPreset;
      if (label) body.blockLabel = label;
      body.showAs = blockPreset === 'oof' ? 'oof' : 'busy';
    }
    const rrule = presetToRRule(recurrence, startDate);
    if (rrule) body.recurrenceRule = rrule;
    const minutes = presetToMinutes(notification);
    if (minutes !== null) body.notificationOffset = minutes;
    if (memo.trim()) body.memo = memo.trim();
    return body;
  }

  /**
   * Build the PATCH payload from the controls the user actually moved.
   *
   * A field present in the body means "set it to this"; a field absent
   * means "leave it alone". Sending the whole form would therefore write
   * back every value the dialog was seeded with, including ones it never
   * had — that is how a title fix used to wipe a long memo. Switching
   * kind counts as touching the kind-specific fields, since those only
   * become meaningful once the new kind is selected.
   */
  function buildPatchBody(): PatchEventInput {
    const calKind = kind as CalEventKind;
    const body: PatchEventInput = {};

    if (anyDirty('title')) body.title = title.trim();

    if (anyDirty(...TIME_FIELDS, 'kind')) {
      body.allDay = calKind === 'milestone' ? true : allDay;
      if (calKind === 'milestone') {
        // Backend enforces (StartAt == nil) != (EndAt == nil); set both.
        body.startAt = allDayToUnix(startDate);
        body.endAt = body.startAt;
      } else if (allDay) {
        body.startAt = allDayToUnix(startDate);
        body.endAt = allDayToUnix(endDate);
      } else {
        body.startAt = wallClockToUnix(startDate, startTime, zone);
        body.endAt = wallClockToUnix(endDate, endTime, zone);
      }
    }
    if (calKind === 'event') {
      if (anyDirty('showAs', 'kind')) body.showAs = showAs;
      if (anyDirty('flexibility', 'kind')) body.flexibility = flexibility;
      if (anyDirty('location', 'kind')) body.location = location.trim();
    }
    if (calKind === 'block' && anyDirty('blockPreset', 'blockCustomLabel', 'kind')) {
      body.blockLabel = blockPreset === 'custom' ? blockCustomLabel.trim() : blockPreset;
      body.showAs = blockPreset === 'oof' ? 'oof' : 'busy';
    }
    // A touched control is an instruction. Picking a preset writes that
    // rule; picking "Does not repeat" clears the stored one, which the
    // API expresses as an explicit clear because an omitted field and a
    // field being emptied look identical in a PATCH. `custom` is the one
    // value that means "leave it": it names a rule this control did not
    // author.
    if (anyDirty('recurrence')) {
      const rrule = presetToRRule(recurrence, startDate);
      if (rrule !== null) {
        body.recurrenceRule = rrule;
      } else if (recurrence === 'none') {
        body.clear = [...(body.clear ?? []), 'recurrenceRule'];
      }
    }
    if (anyDirty('notification')) {
      const minutes = presetToMinutes(notification);
      if (minutes !== null) body.notificationOffset = minutes;
    }
    if (anyDirty('memo')) body.memo = memo.trim();
    return body;
  }

  async function handleDelete(): Promise<void> {
    if (mode.kind !== 'edit') return;
    if (isPending) return;
    // A repeating row is asked which occurrences the delete reaches
    // instead of the plain confirm. That choice already is the
    // confirmation — stacking a second one on top of it only teaches
    // people to click through both.
    if (scopeOccurrenceStart !== null) {
      setScopePrompt({ action: 'delete', occurrenceStart: scopeOccurrenceStart });
      return;
    }
    const confirmed = await confirmAction({
      message: t('action.deleteConfirm', { kind: t(KIND_LABEL_KEYS[kind]) }),
    });
    if (!confirmed) return;
    await commitDelete(null);
  }

  /** Send the DELETE. See {@link commitPatch} for why `scoped` is a pair. */
  async function commitDelete(
    scoped: { scope: RecurrenceScope; occurrenceStart: number } | null,
  ): Promise<void> {
    if (mode.kind !== 'edit') return;
    try {
      await deleteEvent.mutateAsync({
        workspaceId,
        calendarId: mode.calendarId,
        eventId: mode.eventId,
        ...(scoped === null
          ? {}
          : scoped.scope === 'series'
            ? { scope: 'series' as const }
            : { scope: scoped.scope, occurrenceStart: scoped.occurrenceStart }),
      });
      setScopePrompt(null);
      toaster.show({ tone: 'success', message: t(TOAST_DELETED_KEYS[kind]) });
      onSaved();
    } catch (err) {
      setScopePrompt(null);
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'toast.deleteFailed'),
      });
    }
  }

  function handleKeyDown(e: KeyboardEvent<HTMLFormElement>): void {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      void handleSubmit(null);
    }
  }

  /** Any control moved since the dialog opened — gates discard-confirm. */
  function isDirty(): boolean {
    return dirtyFieldsRef.current.size > 0;
  }

  async function handleClose(): Promise<void> {
    if (isPending) return;
    if (isDirty()) {
      const confirmed = await confirmAction({
        message: t('dialog.discard_unsaved'),
        confirmLabel: t('dialog.discard_confirm'),
      });
      if (!confirmed) return;
    }
    onClose();
  }

  if (!open) return null;

  /* ── render ─── */

  const showCalendar = kind !== 'task';
  const showTaskFields = kind === 'task';
  const showEventFields = kind === 'event';
  const showBlockFields = kind === 'block';
  const showMilestone = kind === 'milestone';

  return (
    <>
      <Dialog
        open={open}
        onClose={() => {
          void handleClose();
        }}
        title={headerTitle}
        size="lg"
      >
        <form onSubmit={handleSubmit} onKeyDown={handleKeyDown} className={styles.body}>
          <div className={styles.bodyScroll}>
            <SegmentedControl
              ariaLabel={t('a11y.kind_picker')}
              colourful
              fullWidth
              size="sm"
              options={kindOptions}
              value={kind}
              onChange={editing('kind', setKind)}
            />

            {mode.kind === 'edit' ? (
              <CreatorChip
                displayName={mode.event.creatorDisplayName}
                avatarUrl={mode.event.creatorAvatarUrl}
              />
            ) : null}

            <FormField label={t('field.title')} error={titleError ?? undefined}>
              {(control) => (
                <Input
                  {...control}
                  value={title}
                  onChange={(e) => {
                    markDirty('title');
                    setTitle(e.currentTarget.value);
                  }}
                  placeholder={t(PLACEHOLDER_TITLE_KEYS[kind])}
                  autoFocus={isCreate}
                />
              )}
            </FormField>

            {showCalendar ? (
              <FormField label={t('field.calendar')} error={calendarError ?? undefined}>
                {() => (
                  <Combobox
                    options={calendarOptions}
                    value={calendarId}
                    onChange={editing('calendarId', setCalendarId)}
                    aria-label={t('field.calendar')}
                  />
                )}
              </FormField>
            ) : null}

            {/* Time row — morphs by kind */}
            <div className={styles.timeRow}>
              {showTaskFields ? (
                <>
                  <FormField label={t('field.start')}>
                    {() => (
                      <DatePicker
                        value={startDate}
                        onChange={editing('startDate', setStartDate)}
                        weekdayLabels={weekdayLabels}
                        weekStart={weekStart}
                        formatMonthYear={formatMonthYear}
                        prevLabel={tCommon('calendar.prev')}
                        nextLabel={tCommon('calendar.next')}
                        triggerLabel={
                          startDate
                            ? formatDate(startDate, locale)
                            : tCommon('common.date.placeholder')
                        }
                      />
                    )}
                  </FormField>
                  <span className={styles.arrow} aria-hidden>
                    →
                  </span>
                  <FormField label={t('field.end')}>
                    {() => (
                      <DatePicker
                        value={endDate}
                        onChange={editing('endDate', setEndDate)}
                        weekdayLabels={weekdayLabels}
                        weekStart={weekStart}
                        formatMonthYear={formatMonthYear}
                        prevLabel={tCommon('calendar.prev')}
                        nextLabel={tCommon('calendar.next')}
                        triggerLabel={
                          endDate ? formatDate(endDate, locale) : tCommon('common.date.placeholder')
                        }
                        {...(startDate ? { minDate: startDate } : {})}
                      />
                    )}
                  </FormField>
                </>
              ) : showMilestone ? (
                <FormField label={t('field.date')}>
                  {() => (
                    <DatePicker
                      value={startDate}
                      onChange={(v) => {
                        markDirty('startDate');
                        markDirty('endDate');
                        setStartDate(v);
                        setEndDate(v);
                      }}
                      weekdayLabels={weekdayLabels}
                      weekStart={weekStart}
                      formatMonthYear={formatMonthYear}
                      prevLabel={tCommon('calendar.prev')}
                      nextLabel={tCommon('calendar.next')}
                      triggerLabel={
                        startDate
                          ? formatDate(startDate, locale)
                          : tCommon('common.date.placeholder')
                      }
                    />
                  )}
                </FormField>
              ) : (
                <>
                  <FormField label={t('field.start')} error={timeError ?? undefined}>
                    {() => (
                      <div className={styles.inlineRow}>
                        <div className={styles.inlineGrow}>
                          <DatePicker
                            value={startDate}
                            onChange={editing('startDate', setStartDate)}
                            weekdayLabels={weekdayLabels}
                            weekStart={weekStart}
                            formatMonthYear={formatMonthYear}
                            prevLabel={tCommon('calendar.prev')}
                            nextLabel={tCommon('calendar.next')}
                            triggerLabel={
                              startDate
                                ? formatDate(startDate, locale)
                                : tCommon('common.date.placeholder')
                            }
                          />
                        </div>
                        {allDay ? null : (
                          <TimePicker
                            value={startTime}
                            onChange={editing('startTime', setStartTime)}
                            step={15}
                          />
                        )}
                      </div>
                    )}
                  </FormField>
                  <span className={styles.arrow} aria-hidden>
                    →
                  </span>
                  <FormField label={t('field.end')}>
                    {() => (
                      <div className={styles.inlineRow}>
                        <div className={styles.inlineGrow}>
                          <DatePicker
                            value={endDate}
                            onChange={editing('endDate', setEndDate)}
                            weekdayLabels={weekdayLabels}
                            weekStart={weekStart}
                            formatMonthYear={formatMonthYear}
                            prevLabel={tCommon('calendar.prev')}
                            nextLabel={tCommon('calendar.next')}
                            triggerLabel={
                              endDate
                                ? formatDate(endDate, locale)
                                : tCommon('common.date.placeholder')
                            }
                            {...(startDate ? { minDate: startDate } : {})}
                          />
                        </div>
                        {allDay ? null : (
                          <TimePicker
                            value={endTime}
                            onChange={editing('endTime', setEndTime)}
                            step={15}
                          />
                        )}
                      </div>
                    )}
                  </FormField>
                  {/* Switch first, label after — matches "toggle | what it controls" reading. */}
                  <label htmlFor="nf-event-dialog-allday" className={styles.timeRowAllDay}>
                    <Switch
                      id="nf-event-dialog-allday"
                      checked={allDay}
                      onCheckedChange={handleAllDayChange}
                    />
                    {t('field.allDay')}
                  </label>
                </>
              )}
            </div>

            {/*
             * Kind-specific morph zone. The wrapper has a fixed
             * `min-block-size` matching the tallest variant so the
             * dialog body does not reflow as the user flips the kind
             * picker — the inner `.kindBlock` fades in over the same
             * footprint regardless of which variant is mounted. See
             * `.kindMorphZone` in event-dialog.module.css for the
             * reasoning.
             */}
            <div className={styles.kindMorphZone}>
              {showTaskFields ? (
                <div className={styles.kindBlock}>
                  <div className={styles.inlineRow}>
                    {projects.length > 0 ? (
                      <FormField
                        label={t('field.project')}
                        error={projectError ?? undefined}
                        className={styles.inlineGrow}
                      >
                        {(control) => (
                          <Select
                            {...control}
                            value={projectId}
                            onChange={(e) => {
                              markDirty('projectId');
                              setProjectId(e.currentTarget.value);
                            }}
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
                    <FormField label={t('field.priority')} className={styles.inlineGrow}>
                      {() => (
                        <SegmentedControl
                          ariaLabel={t('field.priority')}
                          options={TASK_PRIORITIES.map((p) => ({
                            value: String(p) as `${TaskPriority}`,
                            label: tCommon(PRIORITY_KEY[p]),
                          }))}
                          value={String(priority) as `${TaskPriority}`}
                          onChange={(v) => {
                            markDirty('priority');
                            setPriority(Number(v) as TaskPriority);
                          }}
                        />
                      )}
                    </FormField>
                  </div>
                </div>
              ) : null}

              {showEventFields ? (
                <div className={styles.kindBlock}>
                  <FormField label={t('field.showAs')}>
                    {() => (
                      <SegmentedControl
                        ariaLabel={t('field.showAs')}
                        fullWidth
                        options={(['busy', 'free', 'tentative', 'oof'] as const).map((v) => ({
                          value: v,
                          label: t(SHOW_AS_KEYS[v]),
                        }))}
                        value={showAs}
                        onChange={editing('showAs', setShowAs)}
                      />
                    )}
                  </FormField>
                  <FormField
                    label={t('field.flexibility')}
                    description={t('field.flexibilityHint')}
                  >
                    {() => (
                      <SegmentedControl
                        ariaLabel={t('field.flexibility')}
                        fullWidth
                        options={(['fixed', 'negotiable', 'conditional'] as const).map((v) => ({
                          value: v,
                          label: t(FLEXIBILITY_KEYS[v]),
                        }))}
                        value={flexibility}
                        onChange={editing('flexibility', setFlexibility)}
                      />
                    )}
                  </FormField>
                  <FormField label={t('field.location')}>
                    {(control) => (
                      <Input
                        {...control}
                        value={location}
                        onChange={(e) => {
                          markDirty('location');
                          setLocation(e.currentTarget.value);
                        }}
                      />
                    )}
                  </FormField>
                </div>
              ) : null}

              {showBlockFields ? (
                <div className={styles.kindBlock}>
                  <FormField label={t('field.blockLabel')}>
                    {() => (
                      <>
                        <ToggleChipGroup label={t('field.blockLabel')}>
                          {(['working', 'focus', 'oof', 'custom'] as const).map((preset) => (
                            <ToggleChip
                              key={preset}
                              pressed={blockPreset === preset}
                              onPressedChange={(v) => {
                                if (!v) return;
                                markDirty('blockPreset');
                                setBlockPreset(preset);
                              }}
                            >
                              {t(BLOCK_PRESET_KEYS[preset])}
                            </ToggleChip>
                          ))}
                        </ToggleChipGroup>
                        {blockPreset === 'custom' ? (
                          <Input
                            value={blockCustomLabel}
                            onChange={(e) => {
                              markDirty('blockCustomLabel');
                              setBlockCustomLabel(e.currentTarget.value);
                            }}
                            style={{ marginBlockStart: 'var(--nf-space-2)' }}
                          />
                        ) : null}
                      </>
                    )}
                  </FormField>
                </div>
              ) : null}
            </div>

            {/* More options disclosure */}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setExpanded((s) => !s)}
              className={styles.disclosureToggle}
              aria-expanded={expanded}
            >
              {t('action.moreOptions')}
            </Button>
            {expanded ? (
              <div className={styles.disclosurePanel}>
                <FormField label={t('field.recurrence')}>
                  {(control) => (
                    <Select
                      {...control}
                      value={recurrence}
                      onChange={(e) => {
                        markDirty('recurrence');
                        setRecurrence(e.currentTarget.value as RecurrencePreset);
                      }}
                    >
                      {/*
                      `custom` is listed only while it is the current
                      value: it describes a stored rule rather than a
                      choice, so offering it on an event that has no
                      such rule would be an option that does nothing.
                    */}
                      {(recurrence === 'custom'
                        ? ([
                            'custom',
                            'none',
                            'daily',
                            'weekdays',
                            'weekly',
                            'monthly',
                            'yearly',
                          ] as const)
                        : (['none', 'daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const)
                      ).map((v) => (
                        <option key={v} value={v}>
                          {/*
                            The weekly option names the day it repeats
                            on, which is the start date's weekday — the
                            other presets carry no placeholder and
                            ignore the value.
                          */}
                          {t(RECURRENCE_PRESET_KEYS[v], { day: weekdayName(startDate, locale) })}
                        </option>
                      ))}
                    </Select>
                  )}
                </FormField>
                <FormField label={t('field.notification')}>
                  {(control) => (
                    <Select
                      {...control}
                      value={notification}
                      onChange={(e) => {
                        markDirty('notification');
                        setNotification(e.currentTarget.value as NotificationPreset);
                      }}
                    >
                      {(
                        [
                          'none',
                          'at_time',
                          '5min',
                          '10min',
                          '15min',
                          '30min',
                          '1hour',
                          '1day',
                        ] as const
                      ).map((v) => (
                        <option key={v} value={v}>
                          {t(NOTIFICATION_PRESET_KEYS[v])}
                        </option>
                      ))}
                    </Select>
                  )}
                </FormField>
                <FormField label={showTaskFields ? t('field.description') : t('field.memo')}>
                  {(control) => (
                    <Textarea
                      {...control}
                      value={memo}
                      onChange={(e) => {
                        markDirty('memo');
                        setMemo(e.currentTarget.value);
                      }}
                      rows={3}
                      // Typing into an empty box before the stored memo
                      // lands would mark the field edited and send that
                      // fragment over the real one, so the control stays
                      // inert until it holds the actual value.
                      disabled={detailLoading}
                      aria-busy={detailLoading || undefined}
                    />
                  )}
                </FormField>
              </div>
            ) : null}
          </div>

          {mode.kind === 'edit' &&
          (mode.initialKind === 'event' || mode.initialKind === 'milestone') ? (
            <AttendeesSection
              workspaceId={workspaceId}
              calendarId={mode.calendarId}
              eventId={mode.eventId}
              ownerUserId={mode.event.ownerUserId ?? null}
              selfUserId={selfUserId}
            />
          ) : null}

          {/* Footer — sibling of the scrolling body so it stays anchored. */}
          <div className={styles.footer}>
            {isEdit ? (
              <Button
                type="button"
                variant="danger"
                onClick={() => {
                  void handleDelete();
                }}
                disabled={isPending}
                aria-label={t('a11y.delete_button')}
              >
                {t('action.delete')}
              </Button>
            ) : null}
            <div className={styles.footerActions}>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  void handleClose();
                }}
                disabled={isPending}
              >
                {t('action.cancel')}
              </Button>
              <Button type="submit" disabled={isPending}>
                {t(isCreate ? 'action.submit.create' : 'action.submit.edit')}
              </Button>
            </div>
          </div>
        </form>
      </Dialog>

      {/*
       * Sibling of the edit dialog rather than a child of its form: the
       * choice is a step of its own, and nothing inside it should reach
       * the form's submit or its Cmd+Enter handler. Mounted only while
       * the question stands, so each ask starts from the default answer.
       */}
      {scopePrompt !== null ? (
        <RecurringScopeDialog
          open
          action={scopePrompt.action}
          pending={isPending}
          onCancel={() => {
            setScopePrompt(null);
          }}
          onConfirm={(scope) => {
            const scoped = { scope, occurrenceStart: scopePrompt.occurrenceStart };
            if (scopePrompt.action === 'delete') void commitDelete(scoped);
            else void commitPatch(scopePrompt.body, scoped);
          }}
        />
      ) : null}
    </>
  );
}

/** Best-effort derivation of the initial date for an edit-mode dialog. */
function inferEditDate(startAt: number | null, zone: Zone): string {
  if (startAt !== null) return unixToWallClock(startAt, zone).date;
  return todayKey(zone);
}
