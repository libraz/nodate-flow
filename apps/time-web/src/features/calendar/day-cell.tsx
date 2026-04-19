import type { HolidayEntry } from '@nodate/holidays';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import type { CalendarEvent } from './types';

interface DayCellProps {
  date: DateTime;
  events: CalendarEvent[];
  isCurrentMonth: boolean;
  isWeekend: boolean;
  holiday: HolidayEntry | null;
  onEventDrop?: (event: CalendarEvent, targetDate: DateTime) => void;
}

const MAX_VISIBLE_EVENTS = 3;

/** Serialize minimal event data for drag transfer. */
function encodeDragData(event: CalendarEvent): string {
  return JSON.stringify({
    id: event.id,
    calendarId: event.calendarId,
    startAt: event.startAt,
    endAt: event.endAt,
    allDay: event.allDay,
  });
}

export default function DayCell(props: DayCellProps): ReactElement {
  const { date, events, isCurrentMonth, holiday, onEventDrop } = props;
  const { t } = useTranslation();
  const { selectedDate, setSelectedDate, openEventModal, openEventDetail } = useCalendarUiStore();
  const [dragOver, setDragOver] = useState(false);

  const isToday = date.hasSame(DateTime.now(), 'day');
  const isSelected = date.hasSame(selectedDate, 'day');
  const visibleEvents = events.slice(0, MAX_VISIBLE_EVENTS);
  const overflowCount = events.length - MAX_VISIBLE_EVENTS;

  const dow = date.weekday % 7; // Sunday=0
  const isSaturday = dow === 6;
  const isSunday = dow === 0;

  let dayNumberColor: string;
  if (isToday) {
    dayNumberColor = '';
  } else if (holiday || isSunday) {
    dayNumberColor = 'var(--nf-cal-sunday)';
  } else if (isSaturday) {
    dayNumberColor = 'var(--nf-cal-saturday)';
  } else {
    dayNumberColor = 'var(--nf-color-fg)';
  }

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    setDragOver(true);
  }, []);

  const handleDragLeave = useCallback(() => {
    setDragOver(false);
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragOver(false);
      const raw = e.dataTransfer.getData('application/x-calendar-event');
      if (!raw || !onEventDrop) return;
      try {
        const data = JSON.parse(raw) as CalendarEvent;
        onEventDrop(data, date);
      } catch {
        // ignore malformed data
      }
    },
    [date, onEventDrop],
  );

  return (
    <div
      onClick={() => setSelectedDate(date)}
      onDoubleClick={() => openEventModal()}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          setSelectedDate(date);
        }
      }}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      className={`day-cell relative flex min-h-[56px] flex-col items-start overflow-hidden border-b border-r border-[var(--nf-color-hairline)] cursor-pointer px-1.5 pt-1.5 pb-1 transition-colors hover:bg-[var(--nf-color-surface-hover)] sm:min-h-[88px] ${
        isSelected ? 'bg-[var(--nf-color-accent-subtle)]' : ''
      } ${!isCurrentMonth ? 'opacity-40' : ''} ${dragOver ? 'ring-2 ring-inset ring-[var(--nf-color-accent)]' : ''}`}
    >
      <div className="flex items-center gap-1">
        {isToday ? (
          <span
            className="flex h-7 w-7 items-center justify-center rounded-full text-[15px] font-medium"
            style={{ backgroundColor: 'var(--nf-color-accent)', color: '#ffffff' }}
          >
            {date.day}
          </span>
        ) : (
          <span
            className="flex h-7 w-7 items-center justify-center text-[15px] font-medium"
            style={dayNumberColor ? { color: dayNumberColor } : undefined}
          >
            {date.day}
          </span>
        )}
        {holiday ? (
          <span
            className="hidden truncate text-[10px] sm:inline"
            style={{ color: 'var(--nf-cal-sunday)' }}
          >
            {holiday.name}
          </span>
        ) : null}
      </div>

      <div className="mt-0.5 w-full space-y-0.5">
        {visibleEvents.map((event) => {
          const color =
            (event as CalendarEvent & { displayColor?: string }).displayColor || '#47B2F7';
          return (
            <button
              key={event.id}
              type="button"
              draggable
              onClick={(e) => {
                e.stopPropagation();
                openEventDetail(event.id);
              }}
              onDragStart={(e) => {
                e.stopPropagation();
                e.dataTransfer.setData('application/x-calendar-event', encodeDragData(event));
                e.dataTransfer.effectAllowed = 'move';
                // Slight delay to let browser render drag ghost before dimming source
                (e.currentTarget as HTMLElement).style.opacity = '0.4';
              }}
              onDragEnd={(e) => {
                (e.currentTarget as HTMLElement).style.opacity = '1';
              }}
              className="mx-0.5 w-full cursor-grab truncate rounded-full border-l-[3px] px-1.5 text-left text-[11px] font-semibold leading-[20px] active:cursor-grabbing"
              style={{
                backgroundColor: `${color}18`,
                borderLeftColor: color,
                color: color,
              }}
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
          <span className="block pl-1 text-[10px]" style={{ color: 'var(--nf-color-fg-muted)' }}>
            {t('event.more', { count: overflowCount })}
          </span>
        ) : null}
      </div>
    </div>
  );
}
