import { LogOut, Settings } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';

import { authSdk } from '../../lib/sdk';
import { selectUser, useAuth } from '../../stores/auth-store';
import { useCalendarUi } from '../../stores/calendar-ui-store';
import { useWorkspace } from '../../stores/workspace-store';
import { useCalendarsQuery } from './api';
import styles from './sidebar.module.css';
import type { Calendar } from './types';

function CalendarItem({ calendar }: { calendar: Calendar }): ReactElement {
  const selectedCalendarIds = useCalendarUi((s) => s.selectedCalendarIds);
  const toggleCalendar = useCalendarUi((s) => s.toggleCalendar);
  const isVisible = selectedCalendarIds.has(calendar.id);

  return (
    <button
      type="button"
      onClick={() => toggleCalendar(calendar.id)}
      className={styles.calendarItem}
    >
      <span
        className={styles.calendarDot}
        style={{
          backgroundColor: calendar.displayColor || calendar.color,
          opacity: isVisible ? 1 : 0.3,
        }}
      />
      <span
        className={styles.calendarName}
        style={{ color: isVisible ? 'var(--nf-color-fg)' : 'var(--nf-color-fg-subtle)' }}
      >
        {calendar.name}
      </span>
    </button>
  );
}

function CalendarSection({
  title,
  calendars,
}: {
  title: string;
  calendars: Calendar[];
}): ReactElement | null {
  if (calendars.length === 0) return null;
  return (
    <div>
      <h3 className={styles.sectionTitle}>{title}</h3>
      {calendars.map((cal) => (
        <CalendarItem key={cal.id} calendar={cal} />
      ))}
    </div>
  );
}

export default function CalendarSidebar(): ReactElement {
  const { t } = useTranslation();
  const { data: calendars, isLoading, isError } = useCalendarsQuery();
  const clearSession = useAuth((s) => s.clearSession);
  const clearWorkspace = useWorkspace((s) => s.clearWorkspace);
  const user = useAuth(selectUser);
  const toggleSettings = useCalendarUi((s) => s.toggleSettings);

  async function handleLogout(): Promise<void> {
    try {
      await authSdk.POST('/auth/logout');
    } catch {
      // Proceed with local cleanup even if the API call fails
    }
    clearSession();
    clearWorkspace();
    window.location.href = '/login';
  }

  if (isLoading) {
    return (
      <aside className={`glass-surface ${styles.sidebar}`}>
        <div className={styles.loadingSidebar}>
          <Skeleton style={{ height: '2rem' }} />
          <Skeleton style={{ height: '2rem' }} />
          <Skeleton style={{ height: '2rem' }} />
        </div>
      </aside>
    );
  }

  const calendarList = calendars ?? [];
  const shared = calendarList.filter((c) => c.kind === 'shared');
  const personal = calendarList.filter((c) => c.kind === 'personal');
  const system = calendarList.filter((c) => c.kind === 'system');
  const hasNoCalendars = calendarList.length === 0;

  return (
    <aside className={`glass-surface ${styles.sidebar}`}>
      <div className={styles.body}>
        {hasNoCalendars ? (
          <div className={styles.empty}>
            <p className={styles.emptyText}>
              {isError ? t('sidebar.loadError') : t('sidebar.noCalendars')}
            </p>
          </div>
        ) : (
          <>
            <CalendarSection title={t('sidebar.sharedCalendars')} calendars={shared} />
            <CalendarSection title={t('sidebar.myCalendar')} calendars={personal} />
            <CalendarSection title={t('sidebar.other')} calendars={system} />
          </>
        )}
      </div>
      <div className={styles.footer}>
        {user && <p className={styles.userEmail}>{user.email}</p>}
        <Button
          variant="ghost"
          size="sm"
          onClick={toggleSettings}
          style={{ width: '100%', justifyContent: 'flex-start', gap: 'var(--nf-space-3)' }}
        >
          <Settings size={16} />
          {t('settings.title')}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={handleLogout}
          style={{ width: '100%', justifyContent: 'flex-start', gap: 'var(--nf-space-3)' }}
        >
          <LogOut size={16} />
          {t('auth.signOut')}
        </Button>
      </div>
    </aside>
  );
}
