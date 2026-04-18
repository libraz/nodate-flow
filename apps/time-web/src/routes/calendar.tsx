import { Navigate, createFileRoute } from '@tanstack/react-router';
import { Plus } from 'lucide-react';
import type { ReactElement } from 'react';

import CalendarGrid from '../features/calendar/calendar-grid';
import CalendarHeader from '../features/calendar/calendar-header';
import EventDetail from '../features/calendar/event-detail';
import EventModal from '../features/calendar/event-modal';
import CalendarSidebar from '../features/calendar/sidebar';
import WeekView from '../features/calendar/week-view';
import { useAuthStore } from '../stores/auth-store';
import { useCalendarUiStore } from '../stores/calendar-ui-store';

export const Route = createFileRoute('/calendar')({
  component: CalendarPage,
});

function CalendarPage(): ReactElement {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const { currentView, sidebarOpen, setSidebarOpen, openEventModal } = useCalendarUiStore();

  if (!isAuthenticated) return <Navigate to="/login" />;

  return (
    <div className="flex h-full flex-col">
      <CalendarHeader />
      <div className="flex flex-1 overflow-hidden">
        {/* Desktop sidebar */}
        <div className="hidden sm:block">
          <CalendarSidebar />
        </div>

        {/* Mobile sidebar drawer */}
        {sidebarOpen ? (
          <>
            <div
              className="fixed inset-0 z-30 bg-black/40 sm:hidden"
              onClick={() => setSidebarOpen(false)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setSidebarOpen(false);
              }}
              role="button"
              tabIndex={-1}
              aria-label="Close sidebar"
            />
            <div className="fixed inset-y-0 left-0 z-30 w-64 bg-white shadow-xl sm:hidden">
              <CalendarSidebar />
            </div>
          </>
        ) : null}

        {/* Calendar content */}
        {currentView === 'week' ? <WeekView /> : <CalendarGrid />}

        {/* Event detail panel */}
        <EventDetail />
      </div>

      {/* Mobile FAB for creating events */}
      <button
        type="button"
        onClick={() => openEventModal()}
        className="fixed bottom-6 right-6 z-20 flex h-14 w-14 items-center justify-center rounded-full bg-blue-600 text-white shadow-lg hover:bg-blue-700 active:scale-95 transition-transform sm:hidden"
        style={{ marginBottom: 'env(safe-area-inset-bottom)' }}
        aria-label="Create new event"
      >
        <Plus className="h-6 w-6" />
      </button>

      <EventModal />
    </div>
  );
}
