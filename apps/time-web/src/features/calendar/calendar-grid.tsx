import { getOrCreateProvider } from '@nodate/holidays';
import { DateTime, Info } from 'luxon';
import { type ReactElement, useMemo } from 'react';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery } from './api';
import DayCell from './day-cell';
import type { CalendarEvent } from './types';

const WEEKDAY_NAMES = Info.weekdays('short', { locale: 'en-US' });
const SUNDAY_FIRST = [WEEKDAY_NAMES[6] ?? 'Sun', ...WEEKDAY_NAMES.slice(0, 6)];

interface MonthDay {
  date: DateTime;
  isCurrentMonth: boolean;
}

function buildMonthGrid(year: number, month: number): MonthDay[] {
  const firstOfMonth = DateTime.local(year, month, 1);
  // Weekday: 1=Monday..7=Sunday in Luxon. We want Sunday=0 start.
  const startDow = firstOfMonth.weekday % 7; // Sunday=0
  const startDate = firstOfMonth.minus({ days: startDow });

  const days: MonthDay[] = [];
  for (let i = 0; i < 42; i++) {
    const date = startDate.plus({ days: i });
    days.push({ date, isCurrentMonth: date.month === month });
  }
  return days;
}

function groupEventsByDay(events: CalendarEvent[]): Map<string, CalendarEvent[]> {
  const map = new Map<string, CalendarEvent[]>();
  for (const event of events) {
    const key = DateTime.fromISO(event.startAt).toISODate();
    if (!key) continue;
    const list = map.get(key);
    if (list) {
      list.push(event);
    } else {
      map.set(key, [event]);
    }
  }
  return map;
}

export default function CalendarGrid(): ReactElement {
  const { selectedDate } = useCalendarUiStore();
  const year = selectedDate.year;
  const month = selectedDate.month;

  const days = useMemo(() => buildMonthGrid(year, month), [year, month]);

  const rangeStart = days[0]?.date.toISODate() ?? '';
  const rangeEnd = days[days.length - 1]?.date.plus({ days: 1 }).toISODate() ?? '';

  const { data: events } = useCalendarEventsQuery(rangeStart, rangeEnd);
  const eventsByDay = useMemo(() => groupEventsByDay(events ?? []), [events]);

  const holidayProvider = useMemo(() => getOrCreateProvider('JP'), []);

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      <div className="grid grid-cols-7 border-b border-gray-200">
        {SUNDAY_FIRST.map((name, i) => (
          <div
            key={name}
            className={`py-2 text-center text-xs font-medium ${
              i === 0 || i === 6 ? 'text-red-500' : 'text-gray-500'
            }`}
          >
            {name}
          </div>
        ))}
      </div>

      <div className="grid flex-1 grid-cols-7 grid-rows-6">
        {days.map((day) => {
          const isoDate = day.date.toISODate() ?? '';
          const dow = day.date.weekday % 7; // Sunday=0
          const isWeekend = dow === 0 || dow === 6;
          const holiday = holidayProvider.isHoliday(day.date.toJSDate());
          return (
            <DayCell
              key={isoDate}
              date={day.date}
              events={eventsByDay.get(isoDate) ?? []}
              isCurrentMonth={day.isCurrentMonth}
              isWeekend={isWeekend}
              holiday={holiday}
            />
          );
        })}
      </div>
    </div>
  );
}
