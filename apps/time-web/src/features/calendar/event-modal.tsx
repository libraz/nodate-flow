import { zodResolver } from '@hookform/resolvers/zod';
import { useQueryClient } from '@tanstack/react-query';
import { DateTime } from 'luxon';
import { type ReactElement, useEffect } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import Button from '@nodate-flow/ui/primitives/button';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Switch from '@nodate-flow/ui/primitives/switch';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import TimePicker from '@nodate-flow/ui/primitives/time-picker';

import { calendarUiStore, useCalendarUi } from '../../stores/calendar-ui-store';
import { useCalendarsQuery, useCreateEventMutation, useUpdateEventMutation } from './api';
import type { CalendarEvent, EventKind, ShowAs } from './types';

const eventFormSchema = z.object({
  title: z.string().min(1, 'event.validation.titleRequired'),
  calendarId: z.string().min(1, 'event.validation.calendarRequired'),
  startDate: z.string().min(1),
  startTime: z.string().min(1),
  endDate: z.string().min(1),
  endTime: z.string().min(1),
  allDay: z.boolean(),
  kind: z.enum(['event', 'block', 'free']),
  showAs: z.enum(['busy', 'free', 'tentative', 'oof']),
  location: z.string(),
  memo: z.string(),
});

type EventFormValues = z.infer<typeof eventFormSchema>;

function toRFC3339(value: string, allDay: boolean): string {
  if (allDay) {
    return `${value}T00:00:00Z`;
  }
  if (!value.includes('T')) {
    return `${value}T00:00:00Z`;
  }
  return DateTime.fromISO(value).toISO() ?? `${value}:00Z`;
}

const WEEKDAYS_JA = ['日', '月', '火', '水', '木', '金', '土'];
const WEEKDAYS_EN = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];

export default function EventModal(): ReactElement | null {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const eventModalOpen = useCalendarUi((s) => s.eventModalOpen);
  const editingEventId = useCalendarUi((s) => s.editingEventId);
  const closeEventModal = useCalendarUi((s) => s.closeEventModal);
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const createMutation = useCreateEventMutation();
  const updateMutation = useUpdateEventMutation();
  const { data: calendars } = useCalendarsQuery();
  const queryClient = useQueryClient();

  const defaultDate = selectedDate.toISODate() ?? DateTime.now().toISODate() ?? '';

  const writableCalendars = (calendars ?? []).filter(
    (c) => c.role === 'owner' || c.role === 'editor' || c.role === 'manager',
  );
  const defaultCalendarId = writableCalendars[0]?.id ?? '';

  const {
    register,
    handleSubmit,
    control,
    reset,
    setValue,
    watch,
    formState: { errors },
  } = useForm<EventFormValues>({
    resolver: zodResolver(eventFormSchema),
    defaultValues: {
      title: '',
      calendarId: defaultCalendarId,
      startDate: defaultDate,
      startTime: '09:00',
      endDate: defaultDate,
      endTime: '10:00',
      allDay: true,
      kind: 'event',
      showAs: 'busy',
      location: '',
      memo: '',
    },
  });

  const allDay = watch('allDay');
  const startDate = watch('startDate');

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

  const weekdayLabels = locale === 'ja' ? WEEKDAYS_JA : WEEKDAYS_EN;

  const formatMonthYear = (year: number, month: number): string => {
    const dt = DateTime.local(year, month, 1).setLocale(locale);
    return dt.toLocaleString({ month: 'long', year: 'numeric' });
  };

  // Set default calendarId when writable calendars load
  useEffect(() => {
    if (defaultCalendarId) {
      setValue('calendarId', defaultCalendarId);
    }
  }, [defaultCalendarId, setValue]);

  // Prefill form when editing an existing event
  useEffect(() => {
    if (!editingEventId || !eventModalOpen) return;
    const queries = queryClient.getQueriesData<CalendarEvent[]>({ queryKey: ['calendars'] });
    let found: CalendarEvent | undefined;
    for (const [, data] of queries) {
      if (!Array.isArray(data)) continue;
      found = data.find((e) => e.id === editingEventId);
      if (found) break;
    }
    if (!found) return;
    const start = DateTime.fromISO(found.startAt);
    const end = DateTime.fromISO(found.endAt);
    reset({
      title: found.title,
      calendarId: found.calendarId ?? defaultCalendarId,
      startDate: start.toISODate() ?? '',
      startTime: start.toFormat('HH:mm'),
      endDate: end.toISODate() ?? '',
      endTime: end.toFormat('HH:mm'),
      allDay: found.allDay,
      kind: found.kind,
      showAs: found.showAs,
      location: found.location ?? '',
      memo: found.memo ?? '',
    });
  }, [editingEventId, eventModalOpen, queryClient, reset, defaultCalendarId]);

  // Prefill start time from time slot click
  useEffect(() => {
    const prefill = calendarUiStore.getState().prefillStartTime;
    if (!prefill || editingEventId) return;
    const dt = DateTime.fromISO(prefill);
    if (dt.isValid) {
      setValue('startDate', dt.toISODate() ?? '');
      setValue('startTime', dt.toFormat('HH:mm'));
      setValue('endDate', dt.toISODate() ?? '');
      setValue('endTime', dt.plus({ hours: 1 }).toFormat('HH:mm'));
      setValue('allDay', false);
    }
  }, [editingEventId, setValue]);

  const onSubmit = (values: EventFormValues): void => {
    const composedStart = values.allDay
      ? values.startDate
      : `${values.startDate}T${values.startTime}`;
    const composedEnd = values.allDay ? values.endDate : `${values.endDate}T${values.endTime}`;

    const timezone = DateTime.local().zoneName;

    const payload = {
      title: values.title,
      calendarId: values.calendarId,
      startAt: toRFC3339(composedStart, values.allDay),
      endAt: toRFC3339(composedEnd, values.allDay),
      allDay: values.allDay,
      kind: values.kind,
      showAs: values.showAs,
      location: values.location || undefined,
      memo: values.memo || undefined,
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
  };

  if (!eventModalOpen) return null;

  const isSubmitting = createMutation.isPending || updateMutation.isPending;

  return (
    <Dialog
      open={eventModalOpen}
      onClose={closeEventModal}
      title={editingEventId ? t('event.editEvent') : t('event.newEvent')}
      fullScreenOnMobile
      style={{ maxInlineSize: '30rem' }}
    >
      <form
        onSubmit={(e) => {
          void handleSubmit(onSubmit)(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}
      >
        {/* Title input */}
        <div>
          <Textarea
            rows={1}
            {...register('title')}
            placeholder={t('event.title')}
            invalid={!!errors.title}
            style={{
              resize: 'none',
              fontSize: 'var(--nf-text-xl)',
              fontWeight: 'var(--nf-weight-light)',
            }}
          />
          {errors.title?.message ? (
            <p
              style={{
                marginBlockStart: 'var(--nf-space-1)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-danger)',
              }}
            >
              {t(errors.title.message)}
            </p>
          ) : null}
        </div>

        {/* Date/time section */}
        <div
          style={{
            borderRadius: 'var(--nf-radius-md)',
            backgroundColor: 'var(--nf-color-bg-sunken)',
            padding: 'var(--nf-space-4)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-2)',
          }}
        >
          {/* All-day toggle */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
              {t('event.allDay')}
            </span>
            <Controller
              control={control}
              name="allDay"
              render={({ field }) => (
                <Switch
                  checked={field.value}
                  onCheckedChange={(checked) => field.onChange(checked)}
                />
              )}
            />
          </div>

          <hr
            style={{
              border: 'none',
              borderBlockStart: '1px solid var(--nf-color-border)',
              opacity: 0.5,
            }}
          />

          {/* Start */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
              {t('event.start')}
            </span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
              <Controller
                control={control}
                name="startDate"
                render={({ field }) => (
                  <DatePicker
                    value={field.value}
                    onChange={field.onChange}
                    weekdayLabels={weekdayLabels}
                    formatMonthYear={formatMonthYear}
                  />
                )}
              />
              {!allDay && (
                <Controller
                  control={control}
                  name="startTime"
                  render={({ field }) => (
                    <TimePicker value={field.value} onChange={field.onChange} />
                  )}
                />
              )}
            </div>
          </div>

          <hr
            style={{
              border: 'none',
              borderBlockStart: '1px solid var(--nf-color-border)',
              opacity: 0.5,
            }}
          />

          {/* End */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
              {t('event.end')}
            </span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
              <Controller
                control={control}
                name="endDate"
                render={({ field }) => (
                  <DatePicker
                    value={field.value}
                    onChange={field.onChange}
                    minDate={startDate}
                    weekdayLabels={weekdayLabels}
                    formatMonthYear={formatMonthYear}
                  />
                )}
              />
              {!allDay && (
                <Controller
                  control={control}
                  name="endTime"
                  render={({ field }) => (
                    <TimePicker value={field.value} onChange={field.onChange} />
                  )}
                />
              )}
            </div>
          </div>
        </div>

        {/* Calendar selector */}
        <div
          style={{
            borderRadius: 'var(--nf-radius-md)',
            backgroundColor: 'var(--nf-color-bg-sunken)',
            padding: 'var(--nf-space-4)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
              {t('event.calendar')}
            </span>
            <Select {...register('calendarId')}>
              {writableCalendars.map((cal) => (
                <option key={cal.id} value={cal.id}>
                  {cal.name}
                </option>
              ))}
            </Select>
          </div>
          {errors.calendarId?.message ? (
            <p
              style={{
                marginBlockStart: 'var(--nf-space-1)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-danger)',
              }}
            >
              {t(errors.calendarId.message)}
            </p>
          ) : null}
        </div>

        {/* Kind & Show As */}
        <div
          style={{
            borderRadius: 'var(--nf-radius-md)',
            backgroundColor: 'var(--nf-color-bg-sunken)',
            padding: 'var(--nf-space-4)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-2)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
              {t('event.kind')}
            </span>
            <Select {...register('kind')}>
              {eventKinds.map((ek) => (
                <option key={ek.value} value={ek.value}>
                  {ek.label}
                </option>
              ))}
            </Select>
          </div>

          <hr
            style={{
              border: 'none',
              borderBlockStart: '1px solid var(--nf-color-border)',
              opacity: 0.5,
            }}
          />

          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
              {t('event.showAs')}
            </span>
            <Select {...register('showAs')}>
              {showAsOptions.map((sa) => (
                <option key={sa.value} value={sa.value}>
                  {sa.label}
                </option>
              ))}
            </Select>
          </div>
        </div>

        {/* Location & Memo */}
        <div
          style={{
            borderRadius: 'var(--nf-radius-md)',
            backgroundColor: 'var(--nf-color-bg-sunken)',
            padding: 'var(--nf-space-4)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-3)',
          }}
        >
          <div>
            <label
              htmlFor="event-location"
              style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}
            >
              {t('event.location')}
            </label>
            <Input
              id="event-location"
              type="text"
              {...register('location')}
              style={{ marginBlockStart: 'var(--nf-space-1)', width: '100%' }}
            />
          </div>

          <hr
            style={{
              border: 'none',
              borderBlockStart: '1px solid var(--nf-color-border)',
              opacity: 0.5,
            }}
          />

          <div>
            <label
              htmlFor="event-memo"
              style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}
            >
              {t('event.memo')}
            </label>
            <Textarea
              id="event-memo"
              {...register('memo')}
              rows={3}
              style={{ marginBlockStart: 'var(--nf-space-1)', width: '100%', resize: 'none' }}
            />
          </div>
        </div>

        {/* Action buttons */}
        <div
          style={{
            display: 'flex',
            gap: 'var(--nf-space-3)',
            borderBlockStart: '1px solid var(--nf-color-border)',
            paddingBlockStart: 'var(--nf-space-4)',
          }}
        >
          <Button type="button" variant="default" onClick={closeEventModal} style={{ flex: 1 }}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={isSubmitting} style={{ flex: 1 }}>
            {isSubmitting ? t('common.saving') : t('common.save')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
