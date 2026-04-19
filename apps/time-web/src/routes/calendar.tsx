import { Navigate, createFileRoute } from '@tanstack/react-router';
import { Calendar, ListTodo, Search, Settings } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import CalendarGrid from '../features/calendar/calendar-grid';
import CalendarHeader from '../features/calendar/calendar-header';
import DayDetail from '../features/calendar/day-detail';
import EventDetail from '../features/calendar/event-detail';
import EventModal from '../features/calendar/event-modal';
import FabButton from '../features/calendar/fab-button';
import RightSidebar from '../features/calendar/right-sidebar';
import SearchPanel from '../features/calendar/search-panel';
import SettingsModal from '../features/calendar/settings-modal';
import CalendarSidebar from '../features/calendar/sidebar';
import WeekView from '../features/calendar/week-view';
import { useAuthStore } from '../stores/auth-store';
import { useCalendarUiStore } from '../stores/calendar-ui-store';

export const Route = createFileRoute('/calendar')({
  component: CalendarPage,
});

function TabButton({
  icon,
  label,
  active,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  active: boolean;
  onClick: () => void;
}): ReactElement {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex flex-1 flex-col items-center gap-0.5 py-2"
      style={{ color: active ? 'var(--nf-color-accent)' : 'var(--nf-color-fg-subtle)' }}
    >
      {icon}
      <span className="text-[10px] font-medium">{label}</span>
    </button>
  );
}

function CalendarPage(): ReactElement {
  const { t } = useTranslation();
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const { currentView, sidebarOpen, setSidebarOpen, mobileTab, setMobileTab } =
    useCalendarUiStore();

  if (!isAuthenticated) return <Navigate to="/login" />;

  return (
    <div className="flex h-full flex-col">
      <CalendarHeader />
      <SearchPanel />
      <div className="flex flex-1 overflow-hidden">
        {/* Desktop sidebar */}
        <div className="hidden sm:flex">
          <CalendarSidebar />
        </div>

        {/* Mobile sidebar drawer */}
        {sidebarOpen ? (
          <>
            <div
              className="fixed inset-0 z-30 bg-[var(--nf-color-overlay)] sm:hidden"
              onClick={() => setSidebarOpen(false)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setSidebarOpen(false);
              }}
              role="button"
              tabIndex={-1}
              aria-label="Close sidebar"
            />
            <div
              className="fixed inset-y-0 left-0 z-30 flex w-64 shadow-xl sm:hidden"
              style={{ backgroundColor: 'var(--nf-color-surface-primary)' }}
            >
              <CalendarSidebar />
            </div>
          </>
        ) : null}

        {/* Calendar content with bottom tab bar padding on mobile */}
        <div className="flex flex-1 flex-col pb-[calc(52px+env(safe-area-inset-bottom))] sm:pb-0">
          {currentView === 'week' ? <WeekView /> : <CalendarGrid />}
        </div>

        {/* Event detail panel */}
        <EventDetail />

        {/* Right sidebar */}
        <div className="hidden sm:block">
          <RightSidebar />
        </div>
      </div>

      {/* Mobile day detail bottom sheet */}
      <DayDetail />

      {/* Mobile FAB for creating events */}
      <FabButton />

      <EventModal />
      <SettingsModal />

      {/* Mobile bottom tab bar */}
      <nav
        className="glass-surface-heavy fixed bottom-0 left-0 right-0 z-40 flex border-t border-[var(--nf-color-border)] sm:hidden"
        style={{ paddingBottom: 'env(safe-area-inset-bottom)' }}
      >
        <TabButton
          icon={<Calendar size={20} />}
          label={t('tabs.calendar')}
          active={mobileTab === 'calendar'}
          onClick={() => setMobileTab('calendar')}
        />
        <TabButton
          icon={<ListTodo size={20} />}
          label={t('tabs.memo')}
          active={mobileTab === 'memo'}
          onClick={() => setMobileTab('memo')}
        />
        <TabButton
          icon={<Search size={20} />}
          label={t('tabs.search')}
          active={mobileTab === 'search'}
          onClick={() => setMobileTab('search')}
        />
        <TabButton
          icon={<Settings size={20} />}
          label={t('tabs.settings')}
          active={mobileTab === 'settings'}
          onClick={() => setMobileTab('settings')}
        />
      </nav>
    </div>
  );
}
