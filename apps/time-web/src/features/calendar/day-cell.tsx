import type { HolidayEntry } from '@nodate-flow/holidays';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { cx } from '@nodate-flow/ui/lib/cx';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import styles from './day-cell.module.css';
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
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const setSelectedDate = useCalendarUi((s) => s.setSelectedDate);
  const openEventModal = useCalendarUi((s) => s.openEventModal);
  const openEventDetail = useCalendarUi((s) => s.openEventDetail);
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
      className={cx(
        styles.cell,
        isSelected && styles.cellSelected,
        !isCurrentMonth && styles.cellFaded,
        dragOver && styles.cellDragOver,
      )}
    >
      <div className={styles.header}>
        {isToday ? (
          <span className={cx(styles.dayNumber, styles.dayNumberToday)}>{date.day}</span>
        ) : (
          <span
            className={styles.dayNumber}
            style={dayNumberColor ? { color: dayNumberColor } : undefined}
          >
            {date.day}
          </span>
        )}
        {holiday ? <span className={styles.holiday}>{holiday.name}</span> : null}
      </div>

      <div className={styles.events}>
        {visibleEvents.map((event) => {
          const color =
            (event as CalendarEvent & { displayColor?: string }).displayColor ||
            'var(--nf-color-accent)';
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
                (e.currentTarget as HTMLElement).style.opacity = '0.4';
              }}
              onDragEnd={(e) => {
                (e.currentTarget as HTMLElement).style.opacity = '1';
              }}
              className={styles.eventChip}
              style={{
                backgroundColor: `${color}1a`,
                color: color,
              }}
            >
              <span className={styles.eventLabelDesktop}>
                {event.allDay
                  ? event.title
                  : `${DateTime.fromISO(event.startAt).toFormat('HH:mm')} ${event.title}`}
              </span>
              <span className={styles.eventLabelMobile}>
                {event.allDay ? event.title : DateTime.fromISO(event.startAt).toFormat('HH:mm')}
              </span>
            </button>
          );
        })}
        {overflowCount > 0 ? (
          <span className={styles.overflow}>{t('event.more', { count: overflowCount })}</span>
        ) : null}
      </div>
    </div>
  );
}
