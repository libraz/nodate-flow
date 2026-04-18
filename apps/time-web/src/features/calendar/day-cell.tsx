import type { HolidayEntry } from '@nodate/holidays';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { getEventClassName, getEventStyle } from './event-styles';
import type { CalendarEvent } from './types';

interface DayCellProps {
  date: DateTime;
  events: CalendarEvent[];
  isCurrentMonth: boolean;
  isWeekend: boolean;
  holiday: HolidayEntry | null;
}

const MAX_VISIBLE_EVENTS = 3;

export default function DayCell({
  date,
  events,
  isCurrentMonth,
  isWeekend,
  holiday,
}: DayCellProps): ReactElement {
  const { selectedDate, setSelectedDate, openEventModal, openEventDetail } = useCalendarUiStore();

  const isToday = date.hasSame(DateTime.now(), 'day');
  const isSelected = date.hasSame(selectedDate, 'day');
  const isNonWorking = isWeekend || holiday != null;
  const visibleEvents = events.slice(0, MAX_VISIBLE_EVENTS);
  const overflowCount = events.length - MAX_VISIBLE_EVENTS;

  const dow = date.weekday % 7; // Sunday=0
  const isSaturday = dow === 6;
  const isSunday = dow === 0;

  let dayNumberColor = '';
  if (isToday) {
    dayNumberColor = '';
  } else if (holiday || isSunday) {
    dayNumberColor = 'text-red-500';
  } else if (isSaturday) {
    dayNumberColor = 'text-blue-500';
  }

  return (
    <button
      type="button"
      onClick={() => setSelectedDate(date)}
      onDoubleClick={() => openEventModal()}
      className={`min-h-16 border-b border-r border-gray-200 p-1 text-sm cursor-pointer transition-colors sm:min-h-24 ${
        isSelected ? 'bg-blue-50' : 'hover:bg-gray-50'
      } ${!isCurrentMonth ? 'opacity-40' : ''} ${isNonWorking && isCurrentMonth ? 'bg-gray-50/60' : ''}`}
    >
      <div className="flex items-center gap-1">
        <span
          className={`inline-flex h-6 w-6 items-center justify-center rounded-full text-xs font-medium ${
            isToday ? 'bg-blue-600 text-white' : dayNumberColor
          }`}
        >
          {date.day}
        </span>
        {holiday ? (
          <span className="hidden truncate text-[10px] text-red-400 sm:inline">{holiday.name}</span>
        ) : null}
      </div>

      <div className="mt-0.5 space-y-0.5">
        {visibleEvents.map((event) => {
          const eventColor = '#3b82f6';
          const style = getEventStyle(event.kind, event.showAs, eventColor);
          const className = getEventClassName(event.kind);
          return (
            <button
              key={event.id}
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                openEventDetail(event.id);
              }}
              className={className}
              style={style}
            >
              <span className="hidden sm:inline">
                {event.allDay
                  ? event.title
                  : `${DateTime.fromISO(event.startAt).toFormat('HH:mm')} ${event.title}`}
              </span>
              <span className="sm:hidden">
                {event.allDay ? event.title : DateTime.fromISO(event.startAt).toFormat('HH:mm')}
              </span>
            </button>
          );
        })}
        {overflowCount > 0 ? (
          <span className="block pl-1 text-[10px] text-gray-500">+{overflowCount} more</span>
        ) : null}
      </div>
    </button>
  );
}
