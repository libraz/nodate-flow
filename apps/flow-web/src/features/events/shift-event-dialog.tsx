/**
 * ShiftEventDialog — two-step confirm UX for shifting an umbrella
 * calendar event together with its linked tasks.
 *
 * API direction:
 *   The umbrella event is the anchor; the actor picks which tasks
 *   travel along when the event shifts. The dialog is anchored on the
 *   event detail page and calls `propose-shift` / `apply-shift` on the
 *   event, not on a task.
 *
 * Step 1 ('pick'):
 *   Datetime input pre-filled with the event's current `startAt`.
 *   "Preview shift" calls {@link useProposeShiftMutation} and, on
 *   success, captures the proposal locally and advances to step 2.
 *
 * Step 2 ('confirm'):
 *   Renders two grouped checkbox lists — `safeTasks` (pre-checked) and
 *   `conflictTasks` (pre-unchecked). The user can toggle any task. The
 *   submit button calls {@link useApplyShiftMutation} with the selected
 *   ids. Apply invalidates the event-detail / calendar / agenda caches.
 *
 * User preference (`me.calendarShiftDefault`):
 *   The dialog respects the current actor's `calendarShiftDefault`
 *   preference (`'ask' | 'sync_always' | 'task_only_always'`) read via
 *   {@link useMeQuery}. `'ask'` (the default for missing/unset values)
 *   keeps the original two-phase flow. `'sync_always'` auto-runs the
 *   proposal at mount with every safe task pre-ticked and jumps
 *   straight to the confirm phase. `'task_only_always'` does the same
 *   but with zero tasks pre-ticked. In both shortcut modes the user
 *   can still toggle individual rows or use Back to change the time.
 *
 *   A trailing "remember this" checkbox lives below the task list in
 *   both phases. When ticked, Apply derives a new preference from the
 *   current safeTask selection state (all = `sync_always`, none =
 *   `task_only_always`, mixed = `ask`) and fires a fire-and-forget
 *   PATCH /me through {@link useUpdateMe}. Success / failure surface
 *   as quiet toasts but never block the apply path.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { TFunction } from 'i18next';
import { type FormEvent, type ReactElement, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type PatchMeInput, useMeQuery, useUpdateMe } from '../settings/api';
import styles from './event-detail-page.module.css';
import {
  type ShiftCandidate,
  type ShiftProposal,
  useApplyShiftMutation,
  useProposeShiftMutation,
} from './shift-api';

type CalendarShiftDefault = 'ask' | 'sync_always' | 'task_only_always';

export interface ShiftEventDialogProps {
  /** Whether the dialog is open. */
  open: boolean;
  /** Called when the dialog requests close (escape, overlay, cancel). */
  onClose: () => void;
  /** Owning workspace id. */
  workspaceId: string;
  /** Owning calendar id (used to invalidate the calendar-events list cache). */
  calendarId: string;
  /** Event being shifted. */
  eventId: string;
  /** Current event start as unix seconds; pre-fills the datetime input. */
  currentStartAt: number;
}

type Phase = 'pick' | 'confirm';

/**
 * Convert a unix-seconds timestamp into the `YYYY-MM-DDTHH:MM` value
 * the native `<input type="datetime-local">` accepts. Always interprets
 * the timestamp in the user's local timezone — the same surface the
 * datetime input itself uses.
 */
function epochToLocalInput(epochSec: number): string {
  const d = new Date(epochSec * 1000);
  const pad = (n: number): string => String(n).padStart(2, '0');
  const y = d.getFullYear();
  const m = pad(d.getMonth() + 1);
  const day = pad(d.getDate());
  const hh = pad(d.getHours());
  const mm = pad(d.getMinutes());
  return `${y}-${m}-${day}T${hh}:${mm}`;
}

/**
 * Parse a `YYYY-MM-DDTHH:MM` value from `<input type="datetime-local">`
 * back into unix seconds. Returns `null` if the value cannot be parsed.
 */
function localInputToEpoch(value: string): number | null {
  if (!value) return null;
  const ms = new Date(value).getTime();
  if (Number.isNaN(ms)) return null;
  return Math.floor(ms / 1000);
}

/**
 * Format a delta in seconds as a sign-aware localised string. Picks
 * the largest unit that's >= 1 (days -> hours -> minutes -> seconds).
 */
function formatDelta(deltaSeconds: number, t: TFunction<'calendar-events'>): string {
  const abs = Math.abs(deltaSeconds);
  let value: string;
  if (abs >= 86_400) {
    value = t('event.shift.confirm.delta_days', { days: Math.round(abs / 86_400) });
  } else if (abs >= 3_600) {
    value = t('event.shift.confirm.delta_hours', { hours: Math.round(abs / 3_600) });
  } else if (abs >= 60) {
    value = t('event.shift.confirm.delta_minutes', { minutes: Math.round(abs / 60) });
  } else {
    value = t('event.shift.confirm.delta_seconds', { seconds: abs });
  }
  if (deltaSeconds < 0) return t('event.shift.confirm.delta_negative', { value });
  if (deltaSeconds > 0) return t('event.shift.confirm.delta_positive', { value });
  return value;
}

/** Render an empty array when the SDK serialises a missing list as `null`. */
function asList(rows: ShiftCandidate[] | null | undefined): ShiftCandidate[] {
  return rows ?? [];
}

interface CandidateRowProps {
  candidate: ShiftCandidate;
  checked: boolean;
  onToggle: (taskId: string) => void;
  formatEventTime: (epochSec: number | undefined) => string;
}

/**
 * One checkbox row inside either the safe or conflict list. Conflict
 * rows render the `otherLinks` muted underneath so the user can see
 * which other events the task is also tied to.
 */
function CandidateRow({
  candidate,
  checked,
  onToggle,
  formatEventTime,
}: CandidateRowProps): ReactElement {
  const otherLinks = candidate.otherLinks ?? [];
  return (
    <li className={styles.shiftCandidateRow}>
      <div className={styles.shiftCandidateLabel}>
        <Checkbox
          checked={checked}
          onChange={() => {
            onToggle(candidate.taskId);
          }}
          aria-label={candidate.taskTitle}
        />
        <span className={styles.shiftCandidateBody}>
          <span className={styles.shiftCandidateTitle}>{candidate.taskTitle}</span>
          {otherLinks.length > 0 ? (
            <span className={styles.shiftCandidateMeta}>
              {otherLinks
                .map((link) => {
                  const when = formatEventTime(link.eventStartAt);
                  return when ? `${link.eventTitle} (${when})` : link.eventTitle;
                })
                .join(' · ')}
            </span>
          ) : null}
        </span>
      </div>
    </li>
  );
}

/**
 * ShiftEventDialog — see file-level docstring.
 */
export default function ShiftEventDialog({
  open,
  onClose,
  workspaceId,
  calendarId,
  eventId,
  currentStartAt,
}: ShiftEventDialogProps): ReactElement {
  const { t, i18n } = useTranslation('calendar-events');
  const locale = i18n.resolvedLanguage ?? 'en';

  const proposeMutation = useProposeShiftMutation();
  const applyMutation = useApplyShiftMutation();
  const meQuery = useMeQuery();
  const updateMe = useUpdateMe();

  const shiftPref: CalendarShiftDefault = meQuery.data.calendarShiftDefault ?? 'ask';

  const [phase, setPhase] = useState<Phase>('pick');
  const [pickValue, setPickValue] = useState<string>(() => epochToLocalInput(currentStartAt));
  const [proposal, setProposal] = useState<ShiftProposal | null>(null);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [rememberDefault, setRememberDefault] = useState<boolean>(false);

  // Reset phase + pre-filled input whenever the dialog reopens against
  // a different event or after a previous flow finished. When the user
  // has a non-`'ask'` `calendarShiftDefault` preference, fire the
  // proposal automatically with the current start time and jump
  // straight to the confirm phase with the appropriate pre-selection.
  useEffect(() => {
    if (!open) return;
    setPhase('pick');
    setPickValue(epochToLocalInput(currentStartAt));
    setProposal(null);
    setSelected(new Set());
    setRememberDefault(false);

    if (shiftPref === 'ask') return;

    let cancelled = false;
    void (async (): Promise<void> => {
      try {
        const result = await proposeMutation.mutateAsync({
          wsId: workspaceId,
          evtId: eventId,
          newStartAt: currentStartAt,
        });
        if (cancelled) return;
        const initial = new Set<string>();
        if (shiftPref === 'sync_always') {
          for (const c of asList(result.safeTasks)) initial.add(c.taskId);
        }
        setProposal(result);
        setSelected(initial);
        setPhase('confirm');
      } catch {
        if (cancelled) return;
        // Fall back to manual pick on failure; the user can retry.
      }
    })();
    return (): void => {
      cancelled = true;
    };
  }, [open, currentStartAt, workspaceId, eventId, shiftPref, proposeMutation.mutateAsync]);

  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'short', timeStyle: 'short' }),
    [locale],
  );

  const formatEventTime = (epochSec: number | undefined): string => {
    if (!epochSec) return '';
    try {
      return dateTimeFormatter.format(new Date(epochSec * 1000));
    } catch {
      return '';
    }
  };

  const handlePreview = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    const newStartAt = localInputToEpoch(pickValue);
    if (newStartAt == null) return;
    try {
      const result = await proposeMutation.mutateAsync({
        wsId: workspaceId,
        evtId: eventId,
        newStartAt,
      });
      setProposal(result);
      // Pre-check every safe task; leave conflicts off by default.
      const initial = new Set<string>();
      for (const c of asList(result.safeTasks)) initial.add(c.taskId);
      setSelected(initial);
      setPhase('confirm');
    } catch {
      toaster.show({ tone: 'danger', message: t('event.shift.pick.preview_error') });
    }
  };

  const toggleSelected = (taskId: string): void => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(taskId)) next.delete(taskId);
      else next.add(taskId);
      return next;
    });
  };

  /**
   * Derive the new `calendarShiftDefault` preference from the current
   * selection state. `safeTasksList` is the ordered list of safe task
   * candidates from the active proposal.
   *   - all safeTasks ticked  → 'sync_always'
   *   - zero safeTasks ticked → 'task_only_always'
   *   - mixed                 → 'ask' (don't pretend mixed maps to a
   *                             fixed pref)
   */
  const deriveShiftDefault = (
    safeTasksList: ShiftCandidate[],
    sel: Set<string>,
  ): CalendarShiftDefault => {
    if (safeTasksList.length === 0) return 'ask';
    let ticked = 0;
    for (const c of safeTasksList) {
      if (sel.has(c.taskId)) ticked += 1;
    }
    if (ticked === safeTasksList.length) return 'sync_always';
    if (ticked === 0) return 'task_only_always';
    return 'ask';
  };

  const handleApply = async (): Promise<void> => {
    if (!proposal) return;
    try {
      const result = await applyMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        newStartAt: proposal.newStartAt,
        confirmedTaskIds: Array.from(selected),
      });
      toaster.show({
        tone: 'success',
        message: t('event.shift.confirm.success', {
          delta: formatDelta(result.deltaSeconds, t),
          count: result.shiftedTasks,
        }),
      });

      // Fire-and-forget preference write. Failures must not block or
      // roll back the apply path — surface a quiet danger toast only.
      // The `...(cond ? { ... } : {})` spread keeps the body compatible
      // with `exactOptionalPropertyTypes` when extending this in future.
      if (rememberDefault) {
        const derived = deriveShiftDefault(asList(proposal.safeTasks), selected);
        const patch: PatchMeInput = { ...{ calendarShiftDefault: derived } };
        updateMe.mutate(patch, {
          onSuccess: () => {
            toaster.show({ tone: 'info', message: t('event.shift.default_saved_toast') });
          },
          onError: () => {
            toaster.show({ tone: 'danger', message: t('event.shift.default_save_error') });
          },
        });
      }

      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('event.shift.confirm.error') });
    }
  };

  const safeTasks = asList(proposal?.safeTasks);
  const conflictTasks = asList(proposal?.conflictTasks);
  const totalCandidates = safeTasks.length + conflictTasks.length;
  const selectedCount = selected.size;

  const canPreview = pickValue.length > 0 && !proposeMutation.isPending;

  const rememberDefaultRow = (
    <label htmlFor="shift-event-remember-default" className={styles.defaultPrefRow}>
      <Checkbox
        id="shift-event-remember-default"
        checked={rememberDefault}
        onChange={(e) => {
          setRememberDefault(e.target.checked);
        }}
      />
      <span>{t('event.shift.default_remember')}</span>
    </label>
  );

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={t('event.shift.dialog.title')}
      size="lg"
      fullScreenOnMobile
    >
      {phase === 'pick' ? (
        <form onSubmit={(e) => void handlePreview(e)} className={styles.shiftForm}>
          <FormField label={t('event.shift.pick.label')} description={t('event.shift.pick.hint')}>
            {(control) => (
              <Input
                {...control}
                type="datetime-local"
                value={pickValue}
                onChange={(e) => {
                  setPickValue(e.target.value);
                }}
                autoFocus
              />
            )}
          </FormField>
          {rememberDefaultRow}
          <div className={styles.shiftActions}>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t('event.shift.dialog.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!canPreview}>
              {t('event.shift.pick.preview')}
            </Button>
          </div>
        </form>
      ) : (
        <div className={styles.shiftForm}>
          <p className={styles.shiftSummary}>
            {totalCandidates === 0
              ? t('event.shift.confirm.no_links')
              : t('event.shift.confirm.summary', {
                  delta: formatDelta(proposal?.deltaSeconds ?? 0, t),
                  selected: selectedCount,
                  total: totalCandidates,
                })}
          </p>

          {safeTasks.length > 0 ? (
            <section className={styles.shiftGroup}>
              <header className={styles.shiftGroupHeader}>
                <h3 className={styles.shiftGroupTitle}>{t('event.shift.confirm.safe_title')}</h3>
                <p className={styles.shiftGroupHint}>{t('event.shift.confirm.safe_hint')}</p>
              </header>
              <ul className={styles.shiftCandidateList}>
                {safeTasks.map((c) => (
                  <CandidateRow
                    key={c.linkId}
                    candidate={c}
                    checked={selected.has(c.taskId)}
                    onToggle={toggleSelected}
                    formatEventTime={formatEventTime}
                  />
                ))}
              </ul>
            </section>
          ) : null}

          {conflictTasks.length > 0 ? (
            <section className={styles.shiftGroup}>
              <header className={styles.shiftGroupHeader}>
                <h3 className={styles.shiftGroupTitle}>
                  {t('event.shift.confirm.conflict_title')}
                </h3>
                <p className={styles.shiftGroupHint}>{t('event.shift.confirm.conflict_hint')}</p>
              </header>
              <ul className={styles.shiftCandidateList}>
                {conflictTasks.map((c) => (
                  <CandidateRow
                    key={c.linkId}
                    candidate={c}
                    checked={selected.has(c.taskId)}
                    onToggle={toggleSelected}
                    formatEventTime={formatEventTime}
                  />
                ))}
              </ul>
            </section>
          ) : null}

          {rememberDefaultRow}

          <div className={styles.shiftActions}>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPhase('pick');
              }}
              disabled={applyMutation.isPending}
            >
              {t('event.shift.dialog.back')}
            </Button>
            <Button
              type="button"
              variant="primary"
              disabled={applyMutation.isPending}
              onClick={() => {
                void handleApply();
              }}
            >
              {applyMutation.isPending
                ? t('event.shift.confirm.applying')
                : t('event.shift.confirm.apply')}
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}
