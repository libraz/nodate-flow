import { useQueryClient } from '@tanstack/react-query';
import { DateTime } from 'luxon';
import { type FormEvent, type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { CustomSelect, DatePickerDropdown, TimePickerDropdown } from '../../components/pickers';
import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarsQuery, useCreateEventMutation, useUpdateEventMutation } from './api';
import type { CalendarEvent, EventKind, ShowAs } from './types';

const eventSchema = z.object({
  title: z.string().min(1),
  calendarId: z.string().min(1),
  startAt: z.string().min(1),
  endAt: z.string().min(1),
  allDay: z.boolean(),
  kind: z.enum(['event', 'block', 'free']),
  showAs: z.enum(['busy', 'free', 'tentative', 'oof']),
  location: z.string().optional(),
  memo: z.string().optional(),
});

function toRFC3339(value: string, allDay: boolean): string {
  if (allDay) {
    return `${value}T00:00:00Z`;
  }
  if (!value.includes('T')) {
    return `${value}T00:00:00Z`;
  }
  return DateTime.fromISO(value).toISO() ?? `${value}:00Z`;
}

export default function EventModal(): ReactElement | null {
  const { t } = useTranslation();
  const { eventModalOpen, editingEventId, closeEventModal, selectedDate } = useCalendarUiStore();
  const createMutation = useCreateEventMutation();
  const updateMutation = useUpdateEventMutation();
  const { data: calendars } = useCalendarsQuery();
  const queryClient = useQueryClient();

  const eventKinds: { value: EventKind; label: string }[] = [
    { value: 'event', label: t('event.kindEvent') },
    { value: 'block', label: t('event.kindBlock') },
    { value: 'free', label: t('event.kindFree') },
  ];

  const showAsOptions: { value: ShowAs; label: string }[] = [
    { value: 'busy', label: t('event.showBusy') },
    { value: 'free', label: t('event.showFree') },
    { value: 'tentative', label: t('event.showTentative') },
    { value: 'oof', label: t('event.showOof') },
  ];

  const defaultDate = selectedDate.toISODate() ?? DateTime.now().toISODate() ?? '';

  const writableCalendars = (calendars ?? []).filter(
    (c) => c.role === 'owner' || c.role === 'editor' || c.role === 'manager',
  );
  const defaultCalendarId = writableCalendars[0]?.id ?? '';

  const [title, setTitle] = useState('');
  const [calendarId, setCalendarId] = useState(defaultCalendarId);
  const [startAt, setStartAt] = useState(defaultDate);
  const [startTime, setStartTime] = useState('09:00');

  useEffect(() => {
    if (!calendarId && defaultCalendarId) {
      setCalendarId(defaultCalendarId);
    }
  }, [calendarId, defaultCalendarId]);
  const [endAt, setEndAt] = useState(defaultDate);
  const [endTime, setEndTime] = useState('10:00');
  const [allDay, setAllDay] = useState(true);
  const [kind, setKind] = useState<EventKind>('event');
  const [showAs, setShowAs] = useState<ShowAs>('busy');
  const [location, setLocation] = useState('');
  const [memo, setMemo] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});

  const handleSubmit = useCallback(
    (e: FormEvent) => {
      e.preventDefault();
      const composedStart = allDay ? startAt : `${startAt}T${startTime}`;
      const composedEnd = allDay ? endAt : `${endAt}T${endTime}`;
      const result = eventSchema.safeParse({
        title,
        calendarId,
        startAt: composedStart,
        endAt: composedEnd,
        allDay,
        kind,
        showAs,
        location: location || undefined,
        memo: memo || undefined,
      });

      if (!result.success) {
        const fieldErrors: Record<string, string> = {};
        for (const issue of result.error.issues) {
          const key = issue.path[0];
          if (typeof key === 'string') {
            fieldErrors[key] = issue.message;
          }
        }
        setErrors(fieldErrors);
        return;
      }

      const timezone = DateTime.local().zoneName;

      const payload = {
        ...result.data,
        startAt: toRFC3339(result.data.startAt, result.data.allDay),
        endAt: toRFC3339(result.data.endAt, result.data.allDay),
        timezone,
      };

      if (editingEventId) {
        updateMutation.mutate(
          { ...payload, eventId: editingEventId },
          { onSuccess: () => closeEventModal() },
        );
      } else {
        createMutation.mutate(payload, { onSuccess: () => closeEventModal() });
      }
    },
    [
      title,
      calendarId,
      startAt,
      startTime,
      endAt,
      endTime,
      allDay,
      kind,
      showAs,
      location,
      memo,
      editingEventId,
      createMutation,
      updateMutation,
      closeEventModal,
    ],
  );

  // Prefill form when editing an existing event
  useEffect(() => {
    if (!editingEventId || !eventModalOpen) return;
    // Search TanStack Query cache for the event
    const queries = queryClient.getQueriesData<CalendarEvent[]>({ queryKey: ['calendars'] });
    let found: CalendarEvent | undefined;
    for (const [, data] of queries) {
      if (!Array.isArray(data)) continue;
      found = data.find((e) => e.id === editingEventId);
      if (found) break;
    }
    if (!found) return;
    setTitle(found.title);
    if (found.calendarId) setCalendarId(found.calendarId);
    const start = DateTime.fromISO(found.startAt);
    const end = DateTime.fromISO(found.endAt);
    setStartAt(start.toISODate() ?? '');
    setStartTime(start.toFormat('HH:mm'));
    setEndAt(end.toISODate() ?? '');
    setEndTime(end.toFormat('HH:mm'));
    setAllDay(found.allDay);
    setKind(found.kind);
    setShowAs(found.showAs);
    setLocation(found.location ?? '');
    setMemo(found.memo ?? '');
  }, [editingEventId, eventModalOpen, queryClient]);

  // Prefill start time from time slot click
  useEffect(() => {
    const prefill = useCalendarUiStore.getState().prefillStartTime;
    if (!prefill || editingEventId) return;
    const dt = DateTime.fromISO(prefill);
    if (dt.isValid) {
      setStartAt(dt.toISODate() ?? '');
      setStartTime(dt.toFormat('HH:mm'));
      setEndAt(dt.toISODate() ?? '');
      setEndTime(dt.plus({ hours: 1 }).toFormat('HH:mm'));
      setAllDay(false);
    }
  }, [editingEventId]);

  if (!eventModalOpen) return null;

  const isSubmitting = createMutation.isPending || updateMutation.isPending;

  return (
    <>
      {/* Backdrop */}
      <div
        className="fixed inset-0 z-50 bg-[var(--color-overlay)]"
        onClick={closeEventModal}
        onKeyDown={(e) => {
          if (e.key === 'Escape') closeEventModal();
        }}
        role="button"
        tabIndex={-1}
        aria-label="Close modal"
      />

      {/* Mobile: bottom sheet */}
      <div className="glass-surface-heavy fixed inset-x-0 bottom-0 z-50 flex max-h-[92vh] flex-col overflow-hidden rounded-t-3xl sm:hidden">
        <div className="mx-auto mt-2 mb-1 h-1 w-10 rounded-full bg-[var(--color-text-tertiary)] opacity-30" />
        <ModalContent
          t={t}
          title={title}
          setTitle={setTitle}
          calendarId={calendarId}
          setCalendarId={setCalendarId}
          startAt={startAt}
          setStartAt={setStartAt}
          startTime={startTime}
          setStartTime={setStartTime}
          endAt={endAt}
          setEndAt={setEndAt}
          endTime={endTime}
          setEndTime={setEndTime}
          allDay={allDay}
          setAllDay={setAllDay}
          kind={kind}
          setKind={setKind}
          showAs={showAs}
          setShowAs={setShowAs}
          location={location}
          setLocation={setLocation}
          memo={memo}
          setMemo={setMemo}
          errors={errors}
          writableCalendars={writableCalendars}
          eventKinds={eventKinds}
          showAsOptions={showAsOptions}
          editingEventId={editingEventId}
          isSubmitting={isSubmitting}
          handleSubmit={handleSubmit}
          closeEventModal={closeEventModal}
        />
      </div>

      {/* Desktop: centered modal */}
      <div className="fixed inset-0 z-50 hidden items-center justify-center sm:flex">
        <div className="glass-surface-heavy flex max-h-[90vh] w-full max-w-[480px] flex-col overflow-hidden rounded-2xl ring-1 ring-[var(--color-border)]">
          <ModalContent
            t={t}
            title={title}
            setTitle={setTitle}
            calendarId={calendarId}
            setCalendarId={setCalendarId}
            startAt={startAt}
            setStartAt={setStartAt}
            startTime={startTime}
            setStartTime={setStartTime}
            endAt={endAt}
            setEndAt={setEndAt}
            endTime={endTime}
            setEndTime={setEndTime}
            allDay={allDay}
            setAllDay={setAllDay}
            kind={kind}
            setKind={setKind}
            showAs={showAs}
            setShowAs={setShowAs}
            location={location}
            setLocation={setLocation}
            memo={memo}
            setMemo={setMemo}
            errors={errors}
            writableCalendars={writableCalendars}
            eventKinds={eventKinds}
            showAsOptions={showAsOptions}
            editingEventId={editingEventId}
            isSubmitting={isSubmitting}
            handleSubmit={handleSubmit}
            closeEventModal={closeEventModal}
          />
        </div>
      </div>
    </>
  );
}

interface ModalContentProps {
  t: (key: string) => string;
  title: string;
  setTitle: (v: string) => void;
  calendarId: string;
  setCalendarId: (v: string) => void;
  startAt: string;
  setStartAt: (v: string) => void;
  startTime: string;
  setStartTime: (v: string) => void;
  endAt: string;
  setEndAt: (v: string) => void;
  endTime: string;
  setEndTime: (v: string) => void;
  allDay: boolean;
  setAllDay: (v: boolean) => void;
  kind: EventKind;
  setKind: (v: EventKind) => void;
  showAs: ShowAs;
  setShowAs: (v: ShowAs) => void;
  location: string;
  setLocation: (v: string) => void;
  memo: string;
  setMemo: (v: string) => void;
  errors: Record<string, string>;
  writableCalendars: { id: string; name: string; color: string }[];

  eventKinds: { value: EventKind; label: string }[];
  showAsOptions: { value: ShowAs; label: string }[];
  editingEventId: string | null;
  isSubmitting: boolean;
  handleSubmit: (e: FormEvent) => void;
  closeEventModal: () => void;
}

function ModalContent({
  t,
  title,
  setTitle,
  calendarId,
  setCalendarId,
  startAt,
  setStartAt,
  startTime,
  setStartTime,
  endAt,
  setEndAt,
  endTime,
  setEndTime,
  allDay,
  setAllDay,
  kind,
  setKind,
  showAs,
  setShowAs,
  location,
  setLocation,
  memo,
  setMemo,
  errors,
  writableCalendars,

  eventKinds,
  showAsOptions,
  editingEventId,
  isSubmitting,
  handleSubmit,
  closeEventModal,
}: ModalContentProps): ReactElement {
  return (
    <form onSubmit={handleSubmit} className="flex flex-1 flex-col overflow-hidden">
      {/* Title input */}
      <div className="px-6 pt-4 pb-2">
        <textarea
          rows={1}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={t('event.title')}
          className="w-full resize-none border-b-2 border-transparent bg-transparent text-[24px] font-light outline-none transition-colors placeholder:text-[var(--color-text-tertiary)] focus:border-[var(--color-accent)]"
          style={{ color: 'var(--color-text-primary)' }}
        />
        {errors.title ? (
          <p className="mt-1 text-[12px]" style={{ color: 'var(--color-danger)' }}>
            {errors.title}
          </p>
        ) : null}
      </div>

      {/* Scrollable body */}
      <div className="flex-1 space-y-4 overflow-y-auto px-6 py-4">
        {/* Date/time card */}
        <div className="rounded-xl bg-[var(--color-surface-secondary)] p-4">
          {/* All-day toggle */}
          <div className="flex items-center justify-between">
            <span className="text-[14px]" style={{ color: 'var(--color-text-secondary)' }}>
              {t('event.allDay')}
            </span>
            <button
              type="button"
              onClick={() => setAllDay(!allDay)}
              className="relative h-[28px] w-[48px] shrink-0 rounded-full transition-colors"
              style={{
                backgroundColor: allDay ? 'var(--color-accent)' : 'var(--color-surface-inset)',
                border: allDay ? 'none' : '1px solid var(--color-border)',
              }}
            >
              <span
                className="absolute left-0 top-[2px] h-[24px] w-[24px] rounded-full shadow-sm transition-transform"
                style={{
                  transform: allDay ? 'translateX(22px)' : 'translateX(2px)',
                  backgroundColor: 'var(--color-surface-elevated, #fff)',
                }}
              />
            </button>
          </div>

          <div className="my-1 border-t border-[var(--color-border)] opacity-50" />

          {/* Start */}
          <div className="flex items-center justify-between py-1">
            <span className="text-[14px]" style={{ color: 'var(--color-text-secondary)' }}>
              {t('event.start')}
            </span>
            <div className="flex items-center gap-2">
              <DatePickerDropdown value={startAt} onChange={setStartAt} />
              {!allDay && <TimePickerDropdown value={startTime} onChange={setStartTime} />}
            </div>
          </div>

          <div className="my-1 border-t border-[var(--color-border)] opacity-50" />

          {/* End */}
          <div className="flex items-center justify-between py-1">
            <span className="text-[14px]" style={{ color: 'var(--color-text-secondary)' }}>
              {t('event.end')}
            </span>
            <div className="flex items-center gap-2">
              <DatePickerDropdown value={endAt} onChange={setEndAt} />
              {!allDay && <TimePickerDropdown value={endTime} onChange={setEndTime} />}
            </div>
          </div>
        </div>

        {/* Calendar selector card */}
        <div className="rounded-xl bg-[var(--color-surface-secondary)] p-4">
          <div className="flex items-center justify-between">
            <span className="text-[14px]" style={{ color: 'var(--color-text-secondary)' }}>
              {t('event.calendar')}
            </span>
            <CustomSelect
              value={calendarId}
              onChange={setCalendarId}
              options={writableCalendars.map((cal) => ({
                value: cal.id,
                label: cal.name,
                icon: (
                  <span
                    className="inline-block h-3 w-3 shrink-0 rounded-full"
                    style={{ backgroundColor: cal.color }}
                  />
                ),
              }))}
              className="w-[200px]"
            />
          </div>
          {errors.calendarId ? (
            <p className="mt-1 text-[12px]" style={{ color: 'var(--color-danger)' }}>
              {errors.calendarId}
            </p>
          ) : null}
        </div>

        {/* Kind & Show As card */}
        <div className="rounded-xl bg-[var(--color-surface-secondary)] p-4">
          <div className="flex items-center justify-between">
            <span className="text-[14px]" style={{ color: 'var(--color-text-secondary)' }}>
              {t('event.kind')}
            </span>
            <CustomSelect
              value={kind}
              onChange={(v) => setKind(v as EventKind)}
              options={eventKinds}
              className="w-[160px]"
            />
          </div>

          <div className="my-1 border-t border-[var(--color-border)] opacity-50" />

          <div className="flex items-center justify-between">
            <span className="text-[14px]" style={{ color: 'var(--color-text-secondary)' }}>
              {t('event.showAs')}
            </span>
            <CustomSelect
              value={showAs}
              onChange={(v) => setShowAs(v as ShowAs)}
              options={showAsOptions}
              className="w-[160px]"
            />
          </div>
        </div>

        {/* Location & Memo card */}
        <div className="rounded-xl bg-[var(--color-surface-secondary)] p-4">
          <div>
            <label
              htmlFor="event-location"
              className="text-[14px]"
              style={{ color: 'var(--color-text-secondary)' }}
            >
              {t('event.location')}
            </label>
            <input
              id="event-location"
              type="text"
              value={location}
              onChange={(e) => setLocation(e.target.value)}
              className="input-modern mt-1 w-full"
            />
          </div>

          <div className="my-1 border-t border-[var(--color-border)] opacity-50" />

          <div>
            <label
              htmlFor="event-memo"
              className="text-[14px]"
              style={{ color: 'var(--color-text-secondary)' }}
            >
              {t('event.memo')}
            </label>
            <textarea
              id="event-memo"
              value={memo}
              onChange={(e) => setMemo(e.target.value)}
              rows={3}
              className="input-modern mt-1 w-full resize-none"
            />
          </div>
        </div>
      </div>

      {/* Action buttons */}
      <div className="flex gap-3 border-t border-[var(--color-border)] px-6 py-4">
        <button type="button" onClick={closeEventModal} className="btn-secondary flex-1 rounded-xl">
          {t('common.cancel')}
        </button>
        <button type="submit" disabled={isSubmitting} className="btn-primary flex-1 rounded-xl">
          {isSubmitting ? t('common.saving') : editingEventId ? t('common.save') : t('common.save')}
        </button>
      </div>
    </form>
  );
}
