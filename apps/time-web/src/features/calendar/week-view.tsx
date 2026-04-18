import { getOrCreateProvider } from '@nodate/holidays';
import { DateTime } from 'luxon';
import { type ReactElement, useEffect, useMemo, useRef } from 'react';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery } from './api';
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

  for (const event of events) {
    const key = DateTime.fromISO(event.startAt).toISODate();
    if (!key) continue;
    if (event.allDay) {
      allDay.get(key)?.push(event);
    } else {
      timed.get(key)?.push(event);
    }
  }

  return { allDay, timed };
}

function CurrentTimeIndicator(): ReactElement | null {
  const now = DateTime.now();
  const minutesSinceStart = now.hour * 60 + now.minute - START_HOUR * 60;
  if (minutesSinceStart < 0 || minutesSinceStart > TOTAL_HOURS * 60) return null;
  const top = (minutesSinceStart / 60) * HOUR_HEIGHT;

  return (
    <div className="pointer-events-none absolute left-0 right-0 z-20" style={{ top }}>
      <div className="flex items-center">
        <div className="h-2.5 w-2.5 shrink-0 rounded-full bg-red-500" />
        <div className="h-0.5 flex-1 bg-red-500" />
      </div>
    </div>
  );
}

export default function WeekView(): ReactElement {
  const { selectedDate, openEventDetail, openEventModal } = useCalendarUiStore();
  const scrollRef = useRef<HTMLDivElement>(null);

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

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      {/* Day header */}
      <div
        className="grid shrink-0 border-b border-gray-200"
        style={{ gridTemplateColumns: '56px repeat(7, 1fr)' }}
      >
        <div className="border-r border-gray-200" />
        {weekDays.map((day) => {
          const iso = day.toISODate() ?? '';
          const isToday = iso === todayIso;
          const dow = day.weekday % 7;
          const holiday = holidayProvider.isHoliday(day.toJSDate());
          const isSunday = dow === 0;
          const isSaturday = dow === 6;

          let textColor = 'text-gray-700';
          if (holiday || isSunday) textColor = 'text-red-500';
          else if (isSaturday) textColor = 'text-blue-500';

          return (
            <div key={iso} className="border-r border-gray-200 px-1 py-2 text-center">
              <div className={`text-xs ${textColor}`}>{day.toFormat('ccc')}</div>
              <div
                className={`mx-auto mt-0.5 flex h-7 w-7 items-center justify-center rounded-full text-sm font-semibold ${
                  isToday ? 'bg-blue-600 text-white' : textColor
                }`}
              >
                {day.day}
              </div>
              {holiday ? (
                <div className="mt-0.5 truncate text-[9px] text-red-400">{holiday.name}</div>
              ) : null}
            </div>
          );
        })}
      </div>

      {/* All-day events bar */}
      {hasAnyAllDay(allDay) ? (
        <div
          className="grid shrink-0 border-b border-gray-200"
          style={{ gridTemplateColumns: '56px repeat(7, 1fr)' }}
        >
          <div className="border-r border-gray-200 px-1 py-1 text-right text-[10px] text-gray-400">
            all-day
          </div>
          {weekDays.map((day) => {
            const iso = day.toISODate() ?? '';
            const dayAllDay = allDay.get(iso) ?? [];
            return (
              <div key={iso} className="border-r border-gray-200 px-0.5 py-0.5 space-y-0.5">
                {dayAllDay.map((event) => (
                  <button
                    key={event.id}
                    type="button"
                    onClick={() => openEventDetail(event.id)}
                    className={getEventClassName(event.kind)}
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
          <div className="relative border-r border-gray-200">
            {HOURS.map((hour) => (
              <div
                key={hour}
                className="absolute right-1 -translate-y-1/2 text-[11px] text-gray-400"
                style={{ top: (hour - START_HOUR) * HOUR_HEIGHT }}
              >
                {DateTime.fromObject({ hour }).toFormat('h a')}
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
                className={`relative border-r border-gray-200 ${isNonWorking ? 'bg-gray-50/60' : ''}`}
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
                    className="absolute left-0 right-0 border-t border-gray-100 cursor-pointer hover:bg-blue-50/40"
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
                      onClick={(e) => {
                        e.stopPropagation();
                        openEventDetail(event.id);
                      }}
                      className="absolute left-0.5 right-0.5 z-10 overflow-hidden rounded px-1 py-0.5 text-left text-[11px] leading-tight"
                      style={{ ...style, top, height, minHeight: 18 }}
                    >
                      <div className="font-medium truncate">{event.title}</div>
                      {height > 30 ? (
                        <div className="truncate opacity-80 text-[10px]">
                          {DateTime.fromISO(event.startAt).toFormat('HH:mm')} -{' '}
                          {DateTime.fromISO(event.endAt).toFormat('HH:mm')}
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
