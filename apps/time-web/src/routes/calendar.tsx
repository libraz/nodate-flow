import { Navigate, createFileRoute } from '@tanstack/react-router';
import { Calendar, ListTodo, Search, Settings } from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
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
import { useCalendarUi } from '../stores/calendar-ui-store';
import styles from './calendar.module.css';

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
      className={styles.tabBtn}
      style={{ color: active ? 'var(--nf-color-accent)' : 'var(--nf-color-fg-subtle)' }}
    >
      {icon}
      <span className={styles.tabLabel}>{label}</span>
    </button>
  );
}

function CalendarPage(): ReactElement {
  const { t } = useTranslation();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const currentView = useCalendarUi((s) => s.currentView);
  const sidebarOpen = useCalendarUi((s) => s.sidebarOpen);
  const setSidebarOpen = useCalendarUi((s) => s.setSidebarOpen);
  const mobileTab = useCalendarUi((s) => s.mobileTab);
  const setMobileTab = useCalendarUi((s) => s.setMobileTab);

  if (!isAuthenticated) return <Navigate to="/login" />;

  return (
    <div className={styles.page}>
      <CalendarHeader />
      <SearchPanel />
      <div className={styles.content}>
        {/* Desktop sidebar */}
        <div className={styles.desktopSidebar}>
          <CalendarSidebar />
        </div>

        {/* Mobile sidebar drawer */}
        {sidebarOpen ? (
          <>
            {/* biome-ignore lint/a11y/useKeyWithClickEvents: backdrop dismissal */}
            <div
              className={styles.sidebarBackdrop}
              onClick={() => setSidebarOpen(false)}
              role="button"
              tabIndex={-1}
              aria-label={t('calendar.close_sidebar')}
            />
            <div className={styles.sidebarDrawer}>
              <CalendarSidebar />
            </div>
          </>
        ) : null}

        {/* Calendar content */}
        <div className={styles.mainArea}>
          {currentView === 'week' ? <WeekView /> : <CalendarGrid />}
        </div>

        {/* Event detail panel */}
        <EventDetail />

        {/* Right sidebar */}
        <div className={styles.desktopRight}>
          <RightSidebar />
        </div>
      </div>

      {/* Mobile day detail bottom sheet */}
      <DayDetail />

      {/* Mobile FAB */}
      <FabButton />

      <EventModal />
      <SettingsModal />

      {/* Mobile bottom tab bar */}
      <nav className={`glass-surface-heavy ${styles.tabBar}`}>
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
