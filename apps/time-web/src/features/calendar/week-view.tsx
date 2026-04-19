import { getOrCreateProvider } from '@nodate/holidays';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery, useUpdateEventMutation } from './api';
import { getEventClassName, getEventStyle } from './event-styles';
import type { CalendarEvent } from './types';

const HOUR_HEIGHT = 60;
const START_HOUR = 6;
const END_HOUR = 22;
const TOTAL_HOURS = END_HOUR - START_HOUR;
const HOURS = Array.from({ length: TOTAL_HOURS }, (_, i) => START_HOUR + i);

function getWeekDays(selectedDate: DateTime): DateTime[] {
  // Sunday-first week
  const dow = selectedDate.weekday % 7; // Sunday=0
  const sunday = selectedDate.minus({ days: dow });
  return Array.from({ length: 7 }, (_, i) => sunday.plus({ days: i }));
}

function getEventTopAndHeight(event: CalendarEvent): { top: number; height: number } {
  const start = DateTime.fromISO(event.startAt);
  const end = DateTime.fromISO(event.endAt);
  const startMinutes = start.hour * 60 + start.minute - START_HOUR * 60;
  const endMinutes = end.hour * 60 + end.minute - START_HOUR * 60;
  const top = Math.max(0, (startMinutes / 60) * HOUR_HEIGHT);
  const height = Math.max(HOUR_HEIGHT / 4, ((endMinutes - startMinutes) / 60) * HOUR_HEIGHT);
  return { top, height };
}

function groupEventsByDay(
  events: CalendarEvent[],
  weekDays: DateTime[],
): { allDay: Map<string, CalendarEvent[]>; timed: Map<string, CalendarEvent[]> } {
  const allDay = new Map<string, CalendarEvent[]>();
  const timed = new Map<string, CalendarEvent[]>();

  for (const day of weekDays) {
    const key = day.toISODate() ?? '';
    allDay.set(key, []);
    timed.set(key, []);
  }

  const weekStart = weekDays[0] ?? DateTime.now();
  const weekEnd = (weekDays[6] ?? DateTime.now()).plus({ days: 1 });

  for (const event of events) {
    const evtStart = DateTime.fromISO(event.startAt);
    const evtEnd = DateTime.fromISO(event.endAt);

    if (event.allDay) {
      let current = evtStart.startOf('day') < weekStart ? weekStart : evtStart.startOf('day');
      const endDay = evtEnd.startOf('day');
      while (current <= endDay && current < weekEnd) {
        const key = current.toISODate();
        if (key) {
          allDay.get(key)?.push(event);
        }
        current = current.plus({ days: 1 });
      }
    } else {
      const key = evtStart.toISODate();
      if (key) {
        timed.get(key)?.push(event);
      }
    }
  }

  return { allDay, timed };
}

function encodeDragData(event: CalendarEvent): string {
  return JSON.stringify({
    id: event.id,
    calendarId: event.calendarId,
    startAt: event.startAt,
    endAt: event.endAt,
    allDay: event.allDay,
  });
}

function CurrentTimeIndicator(): ReactElement | null {
  const now = DateTime.now();
  const minutesSinceStart = now.hour * 60 + now.minute - START_HOUR * 60;
  if (minutesSinceStart < 0 || minutesSinceStart > TOTAL_HOURS * 60) return null;
  const top = (minutesSinceStart / 60) * HOUR_HEIGHT;

  return (
    <div className="pointer-events-none absolute left-0 right-0 z-20" style={{ top }}>
      <div className="flex items-center">
        <div
          className="h-2.5 w-2.5 shrink-0 rounded-full"
          style={{ backgroundColor: 'var(--nf-color-danger)' }}
        />
        <div className="h-0.5 flex-1" style={{ backgroundColor: 'var(--nf-color-danger)' }} />
      </div>
    </div>
  );
}

export default function WeekView(): ReactElement {
  const { t, i18n } = useTranslation();
  const { selectedDate, openEventDetail, openEventModal } = useCalendarUiStore();
  const scrollRef = useRef<HTMLDivElement>(null);
  const updateMutation = useUpdateEventMutation();

  const weekDays = useMemo(() => getWeekDays(selectedDate), [selectedDate]);

  const rangeStart = weekDays[0]?.toISODate() ?? '';
  const rangeEnd = weekDays[6]?.plus({ days: 1 }).toISODate() ?? '';

  const { data: events } = useCalendarEventsQuery(rangeStart, rangeEnd);
  const { allDay, timed } = useMemo(
    () => groupEventsByDay(events ?? [], weekDays),
    [events, weekDays],
  );

  const holidayProvider = useMemo(() => getOrCreateProvider('JP'), []);

  const todayIso = DateTime.now().toISODate();

  // Scroll to current hour on mount
  useEffect(() => {
    if (scrollRef.current) {
      const now = DateTime.now();
      const minutesSinceStart = now.hour * 60 + now.minute - START_HOUR * 60;
      const scrollTo = Math.max(0, (minutesSinceStart / 60) * HOUR_HEIGHT - 120);
      scrollRef.current.scrollTop = scrollTo;
    }
  }, []);

  const handleSlotClick = (day: DateTime, hour: number) => {
    const startTime = day.set({ hour, minute: 0, second: 0 }).toISO() ?? '';
    openEventModal(undefined, startTime);
  };

  const handleEventDrop = useCallback(
    (dragData: CalendarEvent, targetDay: DateTime, targetHour: number) => {
      const origStart = DateTime.fromISO(dragData.startAt);
      const origEnd = DateTime.fromISO(dragData.endAt);
      const duration = origEnd.diff(origStart);

      const newStart = targetDay.set({ hour: targetHour, minute: 0, second: 0 });
      const newEnd = newStart.plus(duration);

      // Skip if dropped on same position
      if (newStart.equals(origStart)) return;

      const timezone = DateTime.local().zoneName;
      updateMutation.mutate({
        eventId: dragData.id,
        calendarId: dragData.calendarId,
        startAt: newStart.toISO() ?? '',
        endAt: newEnd.toISO() ?? '',
        allDay: false,
        timezone,
      });
    },
    [updateMutation],
  );

  const handleAllDayDrop = useCallback(
    (dragData: CalendarEvent, targetDay: DateTime) => {
      const origStart = DateTime.fromISO(dragData.startAt);
      const origEnd = DateTime.fromISO(dragData.endAt);
      const dayDelta = targetDay.startOf('day').diff(origStart.startOf('day'), 'days').days;
      if (dayDelta === 0) return;
      const timezone = DateTime.local().zoneName;
      updateMutation.mutate({
        eventId: dragData.id,
        calendarId: dragData.calendarId,
        startAt: dragData.allDay
          ? `${targetDay.toISODate()}T00:00:00Z`
          : (origStart.plus({ days: dayDelta }).toISO() ?? ''),
        endAt: dragData.allDay
          ? `${targetDay.plus({ days: origEnd.diff(origStart, 'days').days }).toISODate()}T00:00:00Z`
          : (origEnd.plus({ days: dayDelta }).toISO() ?? ''),
        timezone,
      });
    },
    [updateMutation],
  );

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      {/* Day header */}
      <div
        className="grid shrink-0 border-b border-[var(--nf-color-hairline)]"
        style={{ gridTemplateColumns: '56px repeat(7, 1fr)' }}
      >
        <div className="border-r border-[var(--nf-color-hairline)]" />
        {weekDays.map((day) => {
          const iso = day.toISODate() ?? '';
          const isToday = iso === todayIso;
          const dow = day.weekday % 7;
          const holiday = holidayProvider.isHoliday(day.toJSDate());
          const isSunday = dow === 0;
          const isSaturday = dow === 6;

          let dayColor: string;
          if (holiday || isSunday) dayColor = 'var(--nf-cal-sunday)';
          else if (isSaturday) dayColor = 'var(--nf-cal-saturday)';
          else dayColor = 'var(--nf-color-fg)';

          return (
            <div
              key={iso}
              className="border-r border-[var(--nf-color-hairline)] px-1 py-2 text-center"
            >
              <div className="text-xs" style={{ color: dayColor }}>
                {day.setLocale(i18n.language).toLocaleString({ weekday: 'short' })}
              </div>
              <div
                className="mx-auto mt-0.5 flex h-7 w-7 items-center justify-center rounded-full text-sm font-semibold"
                style={
                  isToday
                    ? { backgroundColor: 'var(--nf-color-accent)', color: '#fff' }
                    : { color: dayColor }
                }
              >
                {day.day}
              </div>
              {holiday ? (
                <div
                  className="mt-0.5 truncate text-[9px]"
                  style={{ color: 'var(--nf-cal-sunday)' }}
                >
                  {holiday.name}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>

      {/* All-day events bar */}
      {hasAnyAllDay(allDay) ? (
        <div
          className="grid shrink-0 border-b border-[var(--nf-color-hairline)]"
          style={{ gridTemplateColumns: '56px repeat(7, 1fr)' }}
        >
          <div
            className="border-r border-[var(--nf-color-hairline)] px-1 py-1 text-right text-[10px]"
            style={{ color: 'var(--nf-color-fg-subtle)' }}
          >
            {t('calendar.allDay')}
          </div>
          {weekDays.map((day) => {
            const iso = day.toISODate() ?? '';
            const dayAllDay = allDay.get(iso) ?? [];
            return (
              <div
                key={iso}
                className="border-r border-[var(--nf-color-hairline)] px-0.5 py-0.5 space-y-0.5"
                onDragOver={(e) => {
                  e.preventDefault();
                  e.dataTransfer.dropEffect = 'move';
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const raw = e.dataTransfer.getData('application/x-calendar-event');
                  if (!raw) return;
                  try {
                    const data = JSON.parse(raw) as CalendarEvent;
                    handleAllDayDrop(data, day);
                  } catch {}
                }}
              >
                {dayAllDay.map((event) => (
                  <button
                    key={event.id}
                    type="button"
                    draggable
                    onDragStart={(e) => {
                      e.stopPropagation();
                      e.dataTransfer.setData('application/x-calendar-event', encodeDragData(event));
                      e.dataTransfer.effectAllowed = 'move';
                      (e.currentTarget as HTMLElement).style.opacity = '0.4';
                    }}
                    onDragEnd={(e) => {
                      (e.currentTarget as HTMLElement).style.opacity = '1';
                    }}
                    onClick={() => openEventDetail(event.id)}
                    className={`${getEventClassName(event.kind)} cursor-grab active:cursor-grabbing`}
                    style={getEventStyle(event.kind, event.showAs, '#3b82f6')}
                  >
                    {event.title}
                  </button>
                ))}
              </div>
            );
          })}
        </div>
      ) : null}

      {/* Time grid */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto">
        <div
          className="relative grid"
          style={{
            gridTemplateColumns: '56px repeat(7, 1fr)',
            height: TOTAL_HOURS * HOUR_HEIGHT,
          }}
        >
          {/* Hour labels */}
          <div className="relative border-r border-[var(--nf-color-hairline)]">
            {HOURS.map((hour) => (
              <div
                key={hour}
                className="absolute right-1 -translate-y-1/2 text-[11px]"
                style={{
                  top: (hour - START_HOUR) * HOUR_HEIGHT,
                  color: 'var(--nf-color-fg-subtle)',
                }}
              >
                {DateTime.fromObject({ hour })
                  .setLocale(i18n.language)
                  .toLocaleString({ hour: 'numeric' })}
              </div>
            ))}
          </div>

          {/* Day columns */}
          {weekDays.map((day) => {
            const iso = day.toISODate() ?? '';
            const dayEvents = timed.get(iso) ?? [];
            const isToday = iso === todayIso;
            const dow = day.weekday % 7;
            const holiday = holidayProvider.isHoliday(day.toJSDate());
            const isNonWorking = dow === 0 || dow === 6 || holiday != null;

            return (
              <div
                key={iso}
                className="relative border-r border-[var(--nf-color-hairline)]"
                style={isNonWorking ? { backgroundColor: 'var(--nf-color-bg-sunken)' } : undefined}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.dataTransfer.dropEffect = 'move';
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const raw = e.dataTransfer.getData('application/x-calendar-event');
                  if (!raw) return;
                  const rect = e.currentTarget.getBoundingClientRect();
                  const y = e.clientY - rect.top;
                  const hour = Math.max(
                    START_HOUR,
                    Math.min(END_HOUR - 1, Math.floor(y / HOUR_HEIGHT) + START_HOUR),
                  );
                  try {
                    const data = JSON.parse(raw) as CalendarEvent;
                    handleEventDrop(data, day, hour);
                  } catch {}
                }}
              >
                {/* Hour grid lines */}
                {HOURS.map((hour) => (
                  <div
                    key={hour}
                    role="button"
                    tabIndex={-1}
                    onClick={() => handleSlotClick(day, hour)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') handleSlotClick(day, hour);
                    }}
                    className="absolute left-0 right-0 border-t border-[var(--nf-color-hairline)] cursor-pointer hover:bg-[var(--nf-color-surface-hover)]"
                    style={{
                      top: (hour - START_HOUR) * HOUR_HEIGHT,
                      height: HOUR_HEIGHT,
                    }}
                  />
                ))}

                {/* Events */}
                {dayEvents.map((event) => {
                  const { top, height } = getEventTopAndHeight(event);
                  const style = getEventStyle(event.kind, event.showAs, '#3b82f6');
                  return (
                    <button
                      key={event.id}
                      type="button"
                      draggable
                      onDragStart={(e) => {
                        e.stopPropagation();
                        e.dataTransfer.setData(
                          'application/x-calendar-event',
                          encodeDragData(event),
                        );
                        e.dataTransfer.effectAllowed = 'move';
                        (e.currentTarget as HTMLElement).style.opacity = '0.4';
                      }}
                      onDragEnd={(e) => {
                        (e.currentTarget as HTMLElement).style.opacity = '1';
                      }}
                      onClick={(e) => {
                        e.stopPropagation();
                        openEventDetail(event.id);
                      }}
                      className="absolute left-0.5 right-0.5 z-10 overflow-hidden rounded px-1 py-0.5 text-left text-[11px] leading-tight cursor-grab active:cursor-grabbing"
                      style={{ ...style, top, height, minHeight: 18 }}
                    >
                      <div className="font-medium truncate">{event.title}</div>
                      {height > 30 ? (
                        <div className="truncate opacity-80 text-[10px]">
                          {DateTime.fromISO(event.startAt)
                            .setLocale(i18n.language)
                            .toLocaleString(DateTime.TIME_SIMPLE)}{' '}
                          -{' '}
                          {DateTime.fromISO(event.endAt)
                            .setLocale(i18n.language)
                            .toLocaleString(DateTime.TIME_SIMPLE)}
                        </div>
                      ) : null}
                    </button>
                  );
                })}

                {/* Current time indicator */}
                {isToday ? <CurrentTimeIndicator /> : null}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function hasAnyAllDay(allDay: Map<string, CalendarEvent[]>): boolean {
  for (const events of allDay.values()) {
    if (events.length > 0) return true;
  }
  return false;
}
