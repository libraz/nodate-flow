import { ChevronLeft, ChevronRight, Menu } from 'lucide-react';
import type { ReactElement } from 'react';

import { type CalendarView, useCalendarUiStore } from '../../stores/calendar-ui-store';

const VIEW_LABELS: Record<CalendarView, string> = {
  month: 'Month',
  week: 'Week',
};

export default function CalendarHeader(): ReactElement {
  const {
    selectedDate,
    currentView,
    setCurrentView,
    goToPrevious,
    goToNext,
    goToToday,
    toggleSidebar,
  } = useCalendarUiStore();

  const title =
    currentView === 'week'
      ? `${selectedDate.startOf('week', { useLocaleWeeks: true }).toFormat('MMM d')} - ${selectedDate.endOf('week', { useLocaleWeeks: true }).toFormat('MMM d, yyyy')}`
      : selectedDate.toFormat('MMMM yyyy');

  const prevLabel = currentView === 'week' ? 'Previous week' : 'Previous month';
  const nextLabel = currentView === 'week' ? 'Next week' : 'Next month';

  return (
    <header className="flex items-center justify-between border-b border-gray-200 px-4 py-3">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={toggleSidebar}
          className="rounded-md p-1.5 hover:bg-gray-100 sm:hidden"
          aria-label="Toggle sidebar"
        >
          <Menu className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={goToPrevious}
          className="rounded-md p-1.5 hover:bg-gray-100"
          aria-label={prevLabel}
        >
          <ChevronLeft className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={goToNext}
          className="rounded-md p-1.5 hover:bg-gray-100"
          aria-label={nextLabel}
        >
          <ChevronRight className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={goToToday}
          className="ml-2 rounded-md border border-gray-300 px-3 py-1 text-sm font-medium hover:bg-gray-50"
        >
          Today
        </button>
      </div>

      <h1 className="text-sm font-semibold sm:text-lg">{title}</h1>

      <div className="hidden rounded-md border border-gray-300 sm:flex">
        {(['month', 'week'] as const).map((view) => (
          <button
            key={view}
            type="button"
            onClick={() => setCurrentView(view)}
            className={`px-3 py-1 text-sm font-medium ${
              currentView === view ? 'bg-gray-100 text-gray-900' : 'text-gray-600 hover:bg-gray-50'
            } ${view === 'month' ? 'rounded-l-md' : 'rounded-r-md'}`}
          >
            {VIEW_LABELS[view]}
          </button>
        ))}
      </div>

      {/* Spacer on mobile where toggle is hidden */}
      <div className="w-8 sm:hidden" />
    </header>
  );
}
