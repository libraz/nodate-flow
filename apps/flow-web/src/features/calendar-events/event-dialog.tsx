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

import type { components as flowComponents } from '@nodate-flow/sdk';
import type { components as timeComponents } from '@nodate-flow/time-sdk';
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

import { dateKey } from '../../lib/date-utils';
import { formatDate } from '../../lib/format';
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
  useUpdateEvent,
} from './api';
import AttendeesSection from './attendees-section';
import styles from './event-dialog.module.css';

type FlowProject = flowComponents['schemas']['Project'];
type CalEventLike = timeComponents['schemas']['MyCalendarEventResponse'];

/** Every item kind selectable in the dialog. `task` is flow-api; the others are time-api. */
export type ItemKind = 'task' | 'event' | 'block' | 'free' | 'milestone';

/** Subset of {@link ItemKind} that maps to a calendar event row. */
export type CalEventKind = Exclude<ItemKind, 'task'>;

export type ShowAs = 'busy' | 'free' | 'tentative' | 'oof';

/** Block preset chip identifiers. Maps to `blockLabel` + `showAs` on submit. */
export type BlockPreset = 'working' | 'focus' | 'oof' | 'custom';

/** Recurrence preset identifiers stored in component state. */
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

/** Detect the browser's IANA timezone or fall back to UTC. */
function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
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
 * Translate a recurrence preset to an RRULE-ish payload. `'custom'` is a
 * stub — the caller passes through whatever the user typed in the raw
 * textarea. `'none'` returns null so the caller omits the field.
 *
 * The backend stores `recurrenceRule` as unknown/JSON, so we emit a
 * `{ freq, byDay? }` shape that matches the time-api migration plan
 * without committing to a full RFC 5545 builder. The full builder is
 * out of scope for this PR.
 */
export function presetToRRule(
  preset: RecurrencePreset,
  _startDate: string,
  customRaw?: string,
): unknown {
  switch (preset) {
    case 'none':
      return null;
    case 'daily':
      return { freq: 'DAILY' };
    case 'weekdays':
      return { freq: 'WEEKLY', byDay: ['MO', 'TU', 'WE', 'TH', 'FR'] };
    case 'weekly':
      return { freq: 'WEEKLY' };
    case 'monthly':
      return { freq: 'MONTHLY' };
    case 'yearly':
      return { freq: 'YEARLY' };
    case 'custom':
      return customRaw ? { raw: customRaw } : null;
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
  const [recurrenceCustom, setRecurrenceCustom] = useState<string>('');
  const [notification, setNotification] = useState<NotificationPreset>('none');
  // MyCalendarEventResponse (the `/me/calendar-events` aggregate shape used by
  // the calendar grid) omits `memo`. Starting empty keeps the round-trip
  // idempotent when a full event details endpoint hydrates it later.
  const [memo, setMemo] = useState<string>('');

  // Hydrate notification / recurrence presets on edit.
  useEffect(() => {
    if (mode.kind !== 'edit') return;
    // We don't pull notificationOffset from MyCalendarEventResponse (not
    // returned there). Keep default 'none'; server round-trip fetches
    // full event details if needed. Left as a follow-up.
  }, [mode]);

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

  const headerTitle = t(`dialog.title.${isCreate ? 'create' : 'edit'}.${kind}` as const);

  const kindOptions: SegmentedControlOption<ItemKind>[] = KIND_OPTIONS.map((k) => ({
    value: k,
    label: t(`kind.${k}` as const),
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
        toaster.show({ tone: 'success', message: t(`toast.created.${kind}` as const) });
      } else if (isEdit) {
        const body = buildPatchBody();
        await updateEvent.mutateAsync({
          workspaceId,
          calendarId,
          eventId: mode.eventId,
          body,
        });
        rememberCalendarChoice(workspaceId, calendarId);
        toaster.show({ tone: 'success', message: t(`toast.updated.${kind}` as const) });
      }
      onSaved();
    } catch {
      toaster.show({
        tone: 'danger',
        message: t(isCreate ? 'toast.createFailed' : 'toast.updateFailed'),
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
      // time-api enforces (StartAt == nil) != (EndAt == nil); set both.
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
      if (location.trim()) body.location = location.trim();
    }
    if (calKind === 'block') {
      const label = blockPreset === 'custom' ? blockCustomLabel.trim() : blockPreset;
      if (label) body.blockLabel = label;
      body.showAs = blockPreset === 'oof' ? 'oof' : 'busy';
    }
    const rrule = presetToRRule(recurrence, startDate, recurrenceCustom);
    if (rrule) body.recurrenceRule = rrule;
    const minutes = presetToMinutes(notification);
    if (minutes !== null) body.notificationOffset = minutes;
    if (memo.trim()) body.memo = memo.trim();
    return body;
  }

  function buildPatchBody(): PatchEventInput {
    const calKind = kind as CalEventKind;
    const body: PatchEventInput = {
      title: title.trim(),
      allDay: calKind === 'milestone' ? true : allDay,
    };
    if (calKind === 'milestone') {
      // time-api enforces (StartAt == nil) != (EndAt == nil); set both.
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
      body.location = location.trim();
    }
    if (calKind === 'block') {
      const label = blockPreset === 'custom' ? blockCustomLabel.trim() : blockPreset;
      body.blockLabel = label;
      body.showAs = blockPreset === 'oof' ? 'oof' : 'busy';
    }
    const rrule = presetToRRule(recurrence, startDate, recurrenceCustom);
    if (rrule !== null) body.recurrenceRule = rrule;
    const minutes = presetToMinutes(notification);
    if (minutes !== null) body.notificationOffset = minutes;
    body.memo = memo.trim();
    return body;
  }

  async function handleDelete(): Promise<void> {
    if (mode.kind !== 'edit') return;
    if (isPending) return;
    if (!window.confirm(t('action.deleteConfirm', { kind: t(`kind.${kind}` as const) }))) return;
    try {
      await deleteEvent.mutateAsync({
        workspaceId,
        calendarId: mode.calendarId,
        eventId: mode.eventId,
      });
      toaster.show({ tone: 'success', message: t(`toast.deleted.${kind}` as const) });
      onSaved();
    } catch {
      toaster.show({ tone: 'danger', message: t('toast.deleteFailed') });
    }
  }

  function handleKeyDown(e: KeyboardEvent<HTMLFormElement>): void {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      void handleSubmit(null);
    }
  }

  function handleClose(): void {
    if (isPending) return;
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
    <Dialog open={open} onClose={handleClose} title={headerTitle} size="lg">
      <form onSubmit={handleSubmit} onKeyDown={handleKeyDown} className={styles.body}>
        <div className={styles.bodyScroll}>
          <SegmentedControl
            ariaLabel={t('a11y.kind_picker')}
            colourful
            fullWidth
            size="sm"
            options={kindOptions}
            value={kind}
            onChange={(next) => setKind(next)}
          />

          <FormField label={t('field.title')} error={titleError ?? undefined}>
            {(control) => (
              <Input
                {...control}
                value={title}
                onChange={(e) => setTitle(e.currentTarget.value)}
                placeholder={t(`placeholder.title.${kind}` as const)}
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
                  onChange={setCalendarId}
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
                      onChange={setStartDate}
                      weekdayLabels={weekdayLabels}
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
                      onChange={setEndDate}
                      weekdayLabels={weekdayLabels}
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
                      setStartDate(v);
                      setEndDate(v);
                    }}
                    weekdayLabels={weekdayLabels}
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
                          onChange={setStartDate}
                          weekdayLabels={weekdayLabels}
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
                        <TimePicker value={startTime} onChange={setStartTime} step={15} />
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
                          onChange={setEndDate}
                          weekdayLabels={weekdayLabels}
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
                        <TimePicker value={endTime} onChange={setEndTime} step={15} />
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

          {/* Kind-specific morph zone */}
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
                <FormField label={t('field.priority')} className={styles.inlineGrow}>
                  {() => (
                    <SegmentedControl
                      ariaLabel={t('field.priority')}
                      options={TASK_PRIORITIES.map((p) => ({
                        value: String(p) as `${TaskPriority}`,
                        label: tCommon(PRIORITY_KEY[p]),
                      }))}
                      value={String(priority) as `${TaskPriority}`}
                      onChange={(v) => setPriority(Number(v) as TaskPriority)}
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
                      label: t(`showAs.${v}` as const),
                    }))}
                    value={showAs}
                    onChange={setShowAs}
                  />
                )}
              </FormField>
              <FormField label={t('field.location')}>
                {(control) => (
                  <Input
                    {...control}
                    value={location}
                    onChange={(e) => setLocation(e.currentTarget.value)}
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
                            if (v) setBlockPreset(preset);
                          }}
                        >
                          {t(`blockLabel.preset.${preset}` as const)}
                        </ToggleChip>
                      ))}
                    </ToggleChipGroup>
                    {blockPreset === 'custom' ? (
                      <Input
                        value={blockCustomLabel}
                        onChange={(e) => setBlockCustomLabel(e.currentTarget.value)}
                        style={{ marginBlockStart: 'var(--nf-space-2)' }}
                      />
                    ) : null}
                  </>
                )}
              </FormField>
            </div>
          ) : null}

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
                    onChange={(e) => setRecurrence(e.currentTarget.value as RecurrencePreset)}
                  >
                    {(
                      [
                        'none',
                        'daily',
                        'weekdays',
                        'weekly',
                        'monthly',
                        'yearly',
                        'custom',
                      ] as const
                    ).map((v) => (
                      <option key={v} value={v}>
                        {t(`recurrence.preset.${v}` as const)}
                      </option>
                    ))}
                  </Select>
                )}
              </FormField>
              {recurrence === 'custom' ? (
                <Textarea
                  value={recurrenceCustom}
                  onChange={(e) => setRecurrenceCustom(e.currentTarget.value)}
                  rows={2}
                  placeholder="RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR"
                />
              ) : null}
              <FormField label={t('field.notification')}>
                {(control) => (
                  <Select
                    {...control}
                    value={notification}
                    onChange={(e) => setNotification(e.currentTarget.value as NotificationPreset)}
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
                        {t(`notification.preset.${v}` as const)}
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
                    onChange={(e) => setMemo(e.currentTarget.value)}
                    rows={3}
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
            <Button type="button" variant="ghost" onClick={handleClose} disabled={isPending}>
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
