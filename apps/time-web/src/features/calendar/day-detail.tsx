import { ChevronDown } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useCalendarEventsQuery } from './api';
import type { CalendarEvent } from './types';

function useEventsForSelectedDay(): CalendarEvent[] {
  const { selectedDate, currentView } = useCalendarUiStore();

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
  const { showDayDetail, closeDayDetail, selectedDate, openEventDetail } = useCalendarUiStore();
  const events = useEventsForSelectedDay();

  if (!showDayDetail) return null;

  return (
    <div className="fixed inset-0 z-40 flex flex-col justify-end sm:hidden">
      <div
        className="flex-1"
        onClick={closeDayDetail}
        onKeyDown={(e) => {
          if (e.key === 'Escape') closeDayDetail();
        }}
        role="button"
        tabIndex={-1}
        aria-label={t('calendar.closeDayDetail')}
      />

      <div
        className="glass-surface-heavy rounded-t-[var(--radius-2xl)]"
        style={{ boxShadow: 'var(--shadow-elevated)' }}
      >
        <div className="flex items-center justify-center py-2">
          <button
            type="button"
            onClick={closeDayDetail}
            className="flex items-center justify-center"
            aria-label={t('common.close')}
          >
            <ChevronDown className="h-5 w-5" style={{ color: 'var(--color-text-tertiary)' }} />
          </button>
        </div>

        <div className="px-4 pb-2">
          <h3 className="text-sm font-semibold" style={{ color: 'var(--color-text-primary)' }}>
            {selectedDate
              .setLocale(i18n.language)
              .toLocaleString({ weekday: 'long', month: 'long', day: 'numeric' })}
          </h3>
        </div>

        <div className="max-h-[50vh] overflow-y-auto px-4 pb-[calc(1rem+env(safe-area-inset-bottom))]">
          {events.length === 0 ? (
            <p className="py-8 text-center text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
              {t('calendar.noEventsForDay')}
            </p>
          ) : (
            <div className="space-y-2 pb-2">
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
                    className="flex w-full items-center gap-3 rounded-[var(--radius-sm)] px-3 py-2.5 text-left hover:bg-[var(--color-hover)]"
                  >
                    <div className="h-2.5 w-2.5 shrink-0 rounded-full bg-[var(--color-accent)]" />
                    <div className="min-w-0 flex-1">
                      <p
                        className="truncate text-sm font-medium"
                        style={{ color: 'var(--color-text-primary)' }}
                      >
                        {event.title}
                      </p>
                      <p className="text-xs" style={{ color: 'var(--color-text-secondary)' }}>
                        {event.allDay
                          ? t('event.allDay')
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
