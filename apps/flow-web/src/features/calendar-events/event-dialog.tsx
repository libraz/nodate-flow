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
import { dateKey } from '../../lib/date-utils';
import { formatDate } from '../../lib/format';
import { useWeekStart } from '../../lib/use-week-start';
import { selectUser, useAuth } from '../auth/auth-store';
import { TASK_PRIORITIES, type TaskPriority } from '../tasks/api';
import { PRIORITY_KEY } from '../tasks/constants';
import {
  type CreateEventInput,
  type PatchEventInput,
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
export type RecurrencePreset = 'none' | 'daily' | 'weekdays' | 'weekly' | 'monthly' | 'yearly';

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

export type EventDialogMode =
  | { kind: 'create'; date: string; initialItemKind?: ItemKind }
  | {
      kind: 'edit';
      eventId: string;
      calendarId: string;
      initialKind: CalEventKind;
      event: CalEventLike;
    };

export interface EventDialogProps {
  open: boolean;
  workspaceId: string;
  /** Projects available to the Task kind picker. */
  projects: FlowProject[];
  mode: EventDialogMode;
  onClose: () => void;
  onSaved: () => void;
}

/* ── helpers ────────────────────────────────────────────────────── */

function unixToDateKey(sec: number): string {
  return dateKey(new Date(sec * 1000));
}

function unixToHHMM(sec: number): string {
  const d = new Date(sec * 1000);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

/** Combine a `YYYY-MM-DD` date and a `HH:MM` time into unix seconds (local tz). */
function toUnix(dateStr: string, timeStr: string): number {
  const [y, m, day] = dateStr.split('-').map(Number);
  const [hh, mm] = timeStr.split(':').map(Number);
  const d = new Date(y ?? 1970, (m ?? 1) - 1, day ?? 1, hh ?? 0, mm ?? 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

/**
 * Detect the browser's IANA timezone or fall back to UTC.
 *
 * Cached at module load — the resolved timezone never changes during a
 * session, so calling `Intl.DateTimeFormat()` per render would be waste.
 */
const CACHED_BROWSER_TIMEZONE: string = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
})();

function browserTimezone(): string {
  return CACHED_BROWSER_TIMEZONE;
}

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

  const initialDate = mode.kind === 'create' ? mode.date : inferEditDate(mode.event);

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
  const [endDate, setEndDate] = useState<string>(initialDate);

  const initialTimes = useMemo(() => {
    if (mode.kind === 'edit') {
      const s = mode.event.startAt;
      const e = mode.event.endAt;
      return {
        start: s != null ? unixToHHMM(s) : '09:00',
        end: e != null ? unixToHHMM(e) : '10:00',
      };
    }
    const k: CalEventKind = initialKind === 'task' ? 'event' : initialKind;
    return defaultTimes(k);
  }, [initialKind, mode]);

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
      if (detail.startAt != null) {
        setStartDate(unixToDateKey(detail.startAt));
        setStartTime(unixToHHMM(detail.startAt));
      }
      if (detail.endAt != null) {
        setEndDate(unixToDateKey(detail.endAt));
        setEndTime(unixToHHMM(detail.endAt));
      }
    }
    // A memo sits behind the "more options" disclosure, so an event that
    // has one opens with the panel already down — otherwise the only
    // long-form content on the event is invisible while the user edits.
    if ((detail.memo ?? '') !== '') setExpanded(true);

    // `recurrenceRule` / `notificationOffset` are deliberately not mapped
    // back onto their preset controls: the presets are lossy (an arbitrary
    // rule has no matching preset), so seeding them needs a rule → preset
    // matcher. Until that exists the dirty gate in `buildPatchBody` keeps
    // an untouched control from writing over the stored rule.
  }, [detail, open]);

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
        const s = toUnix(startDate, startTime);
        const e = toUnix(endDate, endTime);
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
      } else if (isCreate) {
        const body = buildCreateBody();
        await createEvent.mutateAsync({ workspaceId, calendarId, body });
        rememberCalendarChoice(workspaceId, calendarId);
        toaster.show({ tone: 'success', message: t(TOAST_CREATED_KEYS[kind]) });
      } else if (isEdit) {
        const body = buildPatchBody();
        // Nothing was touched: skip the round-trip rather than replay the
        // stored values back at the server, which would append an update
        // event and an audit row for a no-op.
        if (Object.keys(body).length > 0) {
          await updateEvent.mutateAsync({
            workspaceId,
            calendarId,
            eventId: mode.eventId,
            body,
          });
          rememberCalendarChoice(workspaceId, calendarId);
        }
        toaster.show({ tone: 'success', message: t(TOAST_UPDATED_KEYS[kind]) });
      }
      onSaved();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, isCreate ? 'toast.createFailed' : 'toast.updateFailed'),
      });
    }
  }

  function buildCreateBody(): CreateEventInput {
    const calKind = kind as CalEventKind;
    const tz = browserTimezone();
    const body: CreateEventInput = {
      title: title.trim(),
      kind: calKind,
      timezone: tz,
      allDay: calKind === 'milestone' ? true : allDay,
    };
    if (calKind === 'milestone') {
      // Backend enforces (StartAt == nil) != (EndAt == nil); set both.
      body.startAt = toUnix(startDate, '00:00');
      body.endAt = body.startAt;
    } else if (allDay) {
      body.startAt = toUnix(startDate, '00:00');
      body.endAt = toUnix(endDate, '23:59');
    } else {
      body.startAt = toUnix(startDate, startTime);
      body.endAt = toUnix(endDate, endTime);
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
        body.startAt = toUnix(startDate, '00:00');
        body.endAt = body.startAt;
      } else if (allDay) {
        body.startAt = toUnix(startDate, '00:00');
        body.endAt = toUnix(endDate, '23:59');
      } else {
        body.startAt = toUnix(startDate, startTime);
        body.endAt = toUnix(endDate, endTime);
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
    // The presets cannot express "clear the stored rule" yet, so an
    // untouched control stays out of the payload entirely rather than
    // asserting 'none' over whatever the event actually has.
    if (anyDirty('recurrence')) {
      const rrule = presetToRRule(recurrence, startDate);
      if (rrule !== null) body.recurrenceRule = rrule;
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
    const confirmed = await confirmAction({
      message: t('action.deleteConfirm', { kind: t(KIND_LABEL_KEYS[kind]) }),
    });
    if (!confirmed) return;
    try {
      await deleteEvent.mutateAsync({
        workspaceId,
        calendarId: mode.calendarId,
        eventId: mode.eventId,
      });
      toaster.show({ tone: 'success', message: t(TOAST_DELETED_KEYS[kind]) });
      onSaved();
    } catch (err) {
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
                      startDate ? formatDate(startDate, locale) : tCommon('common.date.placeholder')
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
                <FormField label={t('field.flexibility')} description={t('field.flexibilityHint')}>
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
                    {(['none', 'daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const).map(
                      (v) => (
                        <option key={v} value={v}>
                          {t(RECURRENCE_PRESET_KEYS[v])}
                        </option>
                      ),
                    )}
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
  );
}

/** Best-effort derivation of the initial date for an edit-mode dialog. */
function inferEditDate(event: CalEventLike): string {
  if (typeof event.startAt === 'number') return unixToDateKey(event.startAt);
  return dateKey(new Date());
}
