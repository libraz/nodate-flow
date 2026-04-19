import { ChevronLeft, ChevronRight, Menu, Plus, Search } from 'lucide-react';
import { DateTime } from 'luxon';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useCalendarUiStore } from '../../stores/calendar-ui-store';

export default function CalendarHeader(): ReactElement {
  const { t, i18n } = useTranslation();
  const {
    selectedDate,
    displayMonth,
    currentView,
    setCurrentView,
    goToPrevious,
    goToNext,
    goToToday,
    toggleSidebar,
    toggleSearch,
    openEventModal,
  } = useCalendarUiStore();

  const title =
    currentView === 'week'
      ? `${selectedDate.startOf('week', { useLocaleWeeks: true }).setLocale(i18n.language).toLocaleString({ month: 'short', day: 'numeric' })} - ${selectedDate.endOf('week', { useLocaleWeeks: true }).setLocale(i18n.language).toLocaleString(DateTime.DATE_MED)}`
      : displayMonth.toLocaleString({ month: 'long', year: 'numeric' });

  const prevLabel =
    currentView === 'week' ? t('calendar.previousWeek') : t('calendar.previousMonth');
  const nextLabel = currentView === 'week' ? t('calendar.nextWeek') : t('calendar.nextMonth');

  return (
    <header className="glass-surface-heavy sticky top-0 z-30 flex h-[56px] items-center justify-between px-3">
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={toggleSidebar}
          className="flex h-8 w-8 items-center justify-center rounded-full hover:bg-[var(--nf-color-surface-hover)] sm:hidden"
          aria-label={t('calendar.toggleSidebar')}
        >
          <Menu className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={goToPrevious}
          className="flex h-8 w-8 items-center justify-center rounded-full hover:bg-[var(--nf-color-surface-hover)]"
          aria-label={prevLabel}
        >
          <ChevronLeft className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={goToNext}
          className="flex h-8 w-8 items-center justify-center rounded-full hover:bg-[var(--nf-color-surface-hover)]"
          aria-label={nextLabel}
        >
          <ChevronRight className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={goToToday}
          className="ml-1 rounded-full bg-[var(--nf-color-accent-subtle)] px-3 text-sm font-medium text-[var(--nf-color-accent)]"
        >
          {t('calendar.today')}
        </button>
      </div>

      <h1 className="text-[16px] font-semibold" style={{ color: 'var(--nf-color-fg)' }}>
        {title}
      </h1>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => openEventModal()}
          className="hidden items-center gap-1 rounded-full px-3 py-1.5 text-sm font-medium text-white transition-opacity hover:opacity-90 sm:flex"
          style={{ background: 'var(--nf-color-accent)' }}
        >
          <Plus className="h-4 w-4" />
          {t('calendar.createNewEvent')}
        </button>
        <button
          type="button"
          onClick={toggleSearch}
          className="flex h-8 w-8 items-center justify-center rounded-full hover:bg-[var(--nf-color-surface-hover)]"
          aria-label={t('search.searchEvents')}
        >
          <Search className="h-5 w-5" />
        </button>
        <div className="segmented-control hidden sm:flex">
          {(['month', 'week'] as const).map((view) => (
            <button
              key={view}
              type="button"
              data-active={currentView === view}
              onClick={() => setCurrentView(view)}
            >
              {t(`calendar.${view}` as const)}
            </button>
          ))}
        </div>
      </div>
    </header>
  );
}
