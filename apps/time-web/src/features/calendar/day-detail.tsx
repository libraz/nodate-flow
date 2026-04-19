import { ChevronDown } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUi } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery } from './api';
import styles from './day-detail.module.css';
import type { CalendarEvent } from './types';

function useEventsForSelectedDay(): CalendarEvent[] {
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const currentView = useCalendarUi((s) => s.currentView);

  const rangeStart = useMemo(() => {
    if (currentView === 'week') {
      const dow = selectedDate.weekday % 7;
      return selectedDate.minus({ days: dow }).toISODate() ?? '';
    }
    const first = DateTime.local(selectedDate.year, selectedDate.month, 1);
    const startDow = first.weekday % 7;
    return first.minus({ days: startDow }).toISODate() ?? '';
  }, [selectedDate, currentView]);

  const rangeEnd = useMemo(() => {
    if (currentView === 'week') {
      const dow = selectedDate.weekday % 7;
      return selectedDate.minus({ days: dow }).plus({ days: 8 }).toISODate() ?? '';
    }
    const first = DateTime.local(selectedDate.year, selectedDate.month, 1);
    const startDow = first.weekday % 7;
    return first.minus({ days: startDow }).plus({ days: 43 }).toISODate() ?? '';
  }, [selectedDate, currentView]);

  const { data: events } = useCalendarEventsQuery(rangeStart, rangeEnd);

  return useMemo(() => {
    if (!events) return [];
    const dayIso = selectedDate.toISODate();
    return events.filter((e) => DateTime.fromISO(e.startAt).toISODate() === dayIso);
  }, [events, selectedDate]);
}

export default function DayDetail(): ReactElement | null {
  const { t, i18n } = useTranslation();
  const showDayDetail = useCalendarUi((s) => s.showDayDetail);
  const closeDayDetail = useCalendarUi((s) => s.closeDayDetail);
  const selectedDate = useCalendarUi((s) => s.selectedDate);
  const openEventDetail = useCalendarUi((s) => s.openEventDetail);
  const events = useEventsForSelectedDay();

  if (!showDayDetail) return null;

  return (
    <div className={styles.overlay}>
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: backdrop dismissal */}
      <div
        className={styles.backdrop}
        onClick={closeDayDetail}
        role="button"
        tabIndex={-1}
        aria-label={t('calendar.close_day_detail')}
      />

      <div className={`glass-surface-heavy ${styles.sheet}`}>
        <div className={styles.handle}>
          <button
            type="button"
            onClick={closeDayDetail}
            style={{
              all: 'unset',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
            aria-label={t('common.close')}
          >
            <ChevronDown size={20} className={styles.handleIcon} />
          </button>
        </div>

        <h3 className={styles.dateTitle}>
          {selectedDate
            .setLocale(i18n.language)
            .toLocaleString({ weekday: 'long', month: 'long', day: 'numeric' })}
        </h3>

        <div className={styles.scrollArea}>
          {events.length === 0 ? (
            <p className={styles.emptyText}>{t('calendar.no_events_for_day')}</p>
          ) : (
            <div className={styles.eventList}>
              {events.map((event) => {
                const start = DateTime.fromISO(event.startAt);
                return (
                  <button
                    key={event.id}
                    type="button"
                    onClick={() => {
                      closeDayDetail();
                      openEventDetail(event.id);
                    }}
                    className={styles.eventItem}
                  >
                    <div className={styles.eventDot} />
                    <div className={styles.eventContent}>
                      <p className={styles.eventTitle}>{event.title}</p>
                      <p className={styles.eventTime}>
                        {event.allDay
                          ? t('event.all_day')
                          : start.setLocale(i18n.language).toLocaleString(DateTime.TIME_SIMPLE)}
                      </p>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
