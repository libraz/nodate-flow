import { getOrCreateProvider } from '@nodate-flow/holidays';
import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { DateTime } from 'luxon';
import {
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useSyncExternalStore,
} from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery, useUpdateEventMutation } from './api';
import { getEventStyle } from './event-styles';
import type { CalendarEvent } from './types';
import styles from './week-view.module.css';

const HOUR_HEIGHT = 60;
const START_HOUR = 6;
const END_HOUR = 22;
const TOTAL_HOURS = END_HOUR - START_HOUR;
const HOURS = Array.from({ length: TOTAL_HOURS }, (_, i) => START_HOUR + i);

/** Number of days to show on mobile (centered on selected date). */
const MOBILE_DAY_COUNT = 3;

const mobileQuery = window.matchMedia(`(max-width: ${BP.sm - 1}px)`);
function subscribeMobile(cb: () => void): () => void {
  mobileQuery.addEventListener('change', cb);
  return () => mobileQuery.removeEventListener('change', cb);
}
function getIsMobile(): boolean {
  return mobileQuery.matches;
}

function useIsMobile(): boolean {
  return useSyncExternalStore(subscribeMobile, getIsMobile, () => false);
}

function getWeekDays(selectedDate: DateTime): DateTime[] {
  const dow = selectedDate.weekday % 7;
  const sunday = selectedDate.minus({ days: dow });
  return Array.from({ length: 7 }, (_, i) => sunday.plus({ days: i }));
}

/** Subset of days centered around selectedDate for mobile view. */
function getMobileDays(selectedDate: DateTime): DateTime[] {
  const half = Math.floor(MOBILE_DAY_COUNT / 2);
  return Array.from({ length: MOBILE_DAY_COUNT }, (_, i) => selectedDate.plus({ days: i - half }));
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
        if (key) allDay.get(key)?.push(event);
        current = current.plus({ days: 1 });
      }
    } else {
      const key = evtStart.toISODate();
      if (key) timed.get(key)?.push(event);
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
    <div className={styles.timeIndicator} style={{ top }}>
      <div className={styles.timeIndicatorLine}>
        <div className={styles.timeIndicatorDot} />
        <div className={styles.timeIndicatorBar} />
      </div>
    </div>
  );
}

export default function WeekView(): ReactElement {
  const { t, i18n } = useTranslation();
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const openEventDetail = useCalendarUi((s) => s.openEventDetail);
  const openEventModal = useCalendarUi((s) => s.openEventModal);
  const scrollRef = useRef<HTMLDivElement>(null);
  const updateMutation = useUpdateEventMutation();
  const isMobile = useIsMobile();

  const weekDays = useMemo(() => getWeekDays(selectedDate), [selectedDate]);
  const visibleDays = useMemo(
    () => (isMobile ? getMobileDays(selectedDate) : weekDays),
    [isMobile, selectedDate, weekDays],
  );

  // Always query the full week range so data is ready if the user resizes
  const rangeStart = weekDays[0]?.toISODate() ?? '';
  const rangeEnd = weekDays[6]?.plus({ days: 1 }).toISODate() ?? '';

  const { data: events } = useCalendarEventsQuery(rangeStart, rangeEnd);
  const { allDay, timed } = useMemo(
    () => groupEventsByDay(events ?? [], visibleDays),
    [events, visibleDays],
  );

  const holidayProvider = useMemo(() => getOrCreateProvider('JP'), []);
  const todayIso = DateTime.now().toISODate();

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

  const dayCount = visibleDays.length;
  const gridCols = isMobile ? `40px repeat(${dayCount}, 1fr)` : `56px repeat(${dayCount}, 1fr)`;

  return (
    <div className={styles.wrapper}>
      {/* Day header */}
      <div className={styles.headerGrid} style={{ gridTemplateColumns: gridCols }}>
        <div className={styles.headerCorner} />
        {visibleDays.map((day) => {
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
            <div key={iso} className={styles.headerDay}>
              <div className={styles.headerWeekday} style={{ color: dayColor }}>
                {day.setLocale(i18n.language).toLocaleString({ weekday: 'short' })}
              </div>
              <div
                className={styles.headerDayNum}
                style={
                  isToday
                    ? {
                        backgroundColor: 'var(--nf-color-accent)',
                        color: 'var(--nf-color-fg-on-accent)',
                      }
                    : { color: dayColor }
                }
              >
                {day.day}
              </div>
              {holiday ? <div className={styles.headerHoliday}>{holiday.name}</div> : null}
            </div>
          );
        })}
      </div>

      {/* All-day events bar */}
      {hasAnyAllDay(allDay) ? (
        <div className={styles.allDayGrid} style={{ gridTemplateColumns: gridCols }}>
          <div className={styles.allDayLabel}>{t('calendar.all_day')}</div>
          {visibleDays.map((day) => {
            const iso = day.toISODate() ?? '';
            const dayAllDay = allDay.get(iso) ?? [];
            return (
              <div
                key={iso}
                className={styles.allDayCol}
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
                    className={styles.allDayEvent}
                    style={getEventStyle(event.kind, event.showAs, 'var(--nf-color-accent)')}
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
      <div ref={scrollRef} className={styles.scrollArea}>
        <div
          className={styles.timeGrid}
          style={{ gridTemplateColumns: gridCols, height: TOTAL_HOURS * HOUR_HEIGHT }}
        >
          {/* Hour labels */}
          <div className={styles.hourLabelCol}>
            {HOURS.map((hour) => (
              <div
                key={hour}
                className={styles.hourLabel}
                style={{ top: (hour - START_HOUR) * HOUR_HEIGHT }}
              >
                {DateTime.fromObject({ hour })
                  .setLocale(i18n.language)
                  .toLocaleString({ hour: 'numeric' })}
              </div>
            ))}
          </div>

          {/* Day columns */}
          {visibleDays.map((day) => {
            const iso = day.toISODate() ?? '';
            const dayEvents = timed.get(iso) ?? [];
            const isToday = iso === todayIso;
            const dow = day.weekday % 7;
            const holiday = holidayProvider.isHoliday(day.toJSDate());
            const isNonWorking = dow === 0 || dow === 6 || holiday != null;

            return (
              <div
                key={iso}
                className={styles.dayCol}
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
                    className={styles.hourSlot}
                    style={{
                      top: (hour - START_HOUR) * HOUR_HEIGHT,
                      height: HOUR_HEIGHT,
                    }}
                  />
                ))}

                {/* Events */}
                {dayEvents.map((event) => {
                  const { top, height } = getEventTopAndHeight(event);
                  const style = getEventStyle(event.kind, event.showAs, 'var(--nf-color-accent)');
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
                      className={styles.timedEvent}
                      style={{ ...style, top, height, minHeight: 18 }}
                    >
                      <div className={styles.timedEventTitle}>{event.title}</div>
                      {height > 30 ? (
                        <div className={styles.timedEventTime}>
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
