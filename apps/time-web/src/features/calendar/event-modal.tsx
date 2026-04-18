import { DateTime } from 'luxon';
import { type FormEvent, type ReactElement, useCallback, useState } from 'react';
import { z } from 'zod';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCreateEventMutation, useUpdateEventMutation } from './api';
import type { EventKind, ShowAs } from './types';

const eventSchema = z.object({
  title: z.string().min(1, 'Title is required'),
  calendarId: z.string().min(1, 'Calendar is required'),
  startAt: z.string().min(1, 'Start date is required'),
  endAt: z.string().min(1, 'End date is required'),
  allDay: z.boolean(),
  kind: z.enum(['event', 'block', 'free']),
  showAs: z.enum(['busy', 'free', 'tentative', 'oof']),
  location: z.string().optional(),
  memo: z.string().optional(),
});

const EVENT_KINDS: { value: EventKind; label: string }[] = [
  { value: 'event', label: 'Event' },
  { value: 'block', label: 'Block' },
  { value: 'free', label: 'Free' },
];

const SHOW_AS_OPTIONS: { value: ShowAs; label: string }[] = [
  { value: 'busy', label: 'Busy' },
  { value: 'free', label: 'Free' },
  { value: 'tentative', label: 'Tentative' },
  { value: 'oof', label: 'Out of office' },
];

export default function EventModal(): ReactElement | null {
  const { eventModalOpen, editingEventId, closeEventModal, selectedDate } = useCalendarUiStore();
  const createMutation = useCreateEventMutation();
  const updateMutation = useUpdateEventMutation();

  const defaultDate = selectedDate.toISODate() ?? DateTime.now().toISODate() ?? '';

  const [title, setTitle] = useState('');
  const [calendarId, setCalendarId] = useState('');
  const [startAt, setStartAt] = useState(defaultDate);
  const [endAt, setEndAt] = useState(defaultDate);
  const [allDay, setAllDay] = useState(true);
  const [kind, setKind] = useState<EventKind>('event');
  const [showAs, setShowAs] = useState<ShowAs>('busy');
  const [location, setLocation] = useState('');
  const [memo, setMemo] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});

  const handleSubmit = useCallback(
    (e: FormEvent) => {
      e.preventDefault();
      const result = eventSchema.safeParse({
        title,
        calendarId,
        startAt,
        endAt,
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

      if (editingEventId) {
        updateMutation.mutate(
          { ...result.data, eventId: editingEventId, timezone },
          { onSuccess: () => closeEventModal() },
        );
      } else {
        createMutation.mutate({ ...result.data, timezone }, { onSuccess: () => closeEventModal() });
      }
    },
    [
      title,
      calendarId,
      startAt,
      endAt,
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

  if (!eventModalOpen) return null;

  const isSubmitting = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-full max-w-lg rounded-lg bg-white p-6 shadow-xl">
        <h2 className="mb-4 text-lg font-semibold">
          {editingEventId ? 'Edit Event' : 'New Event'}
        </h2>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label htmlFor="event-title" className="block text-sm font-medium text-gray-700">
              Title
            </label>
            <input
              id="event-title"
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            {errors.title ? <p className="mt-1 text-xs text-red-500">{errors.title}</p> : null}
          </div>

          <div>
            <label htmlFor="event-calendar" className="block text-sm font-medium text-gray-700">
              Calendar ID
            </label>
            <input
              id="event-calendar"
              type="text"
              value={calendarId}
              onChange={(e) => setCalendarId(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            {errors.calendarId ? (
              <p className="mt-1 text-xs text-red-500">{errors.calendarId}</p>
            ) : null}
          </div>

          <div className="flex items-center gap-3">
            <label htmlFor="event-allday" className="flex items-center gap-2 text-sm">
              <input
                id="event-allday"
                type="checkbox"
                checked={allDay}
                onChange={(e) => setAllDay(e.target.checked)}
                className="rounded border-gray-300"
              />
              All day
            </label>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label htmlFor="event-start" className="block text-sm font-medium text-gray-700">
                Start
              </label>
              <input
                id="event-start"
                type={allDay ? 'date' : 'datetime-local'}
                value={startAt}
                onChange={(e) => setStartAt(e.target.value)}
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label htmlFor="event-end" className="block text-sm font-medium text-gray-700">
                End
              </label>
              <input
                id="event-end"
                type={allDay ? 'date' : 'datetime-local'}
                value={endAt}
                onChange={(e) => setEndAt(e.target.value)}
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label htmlFor="event-kind" className="block text-sm font-medium text-gray-700">
                Kind
              </label>
              <select
                id="event-kind"
                value={kind}
                onChange={(e) => setKind(e.target.value as EventKind)}
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                {EVENT_KINDS.map((k) => (
                  <option key={k.value} value={k.value}>
                    {k.label}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label htmlFor="event-showas" className="block text-sm font-medium text-gray-700">
                Show as
              </label>
              <select
                id="event-showas"
                value={showAs}
                onChange={(e) => setShowAs(e.target.value as ShowAs)}
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                {SHOW_AS_OPTIONS.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div>
            <label htmlFor="event-location" className="block text-sm font-medium text-gray-700">
              Location
            </label>
            <input
              id="event-location"
              type="text"
              value={location}
              onChange={(e) => setLocation(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label htmlFor="event-memo" className="block text-sm font-medium text-gray-700">
              Memo
            </label>
            <textarea
              id="event-memo"
              value={memo}
              onChange={(e) => setMemo(e.target.value)}
              rows={3}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={closeEventModal}
              className="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {isSubmitting ? 'Saving...' : 'Save'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
