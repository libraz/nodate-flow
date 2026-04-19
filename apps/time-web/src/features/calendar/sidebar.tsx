import { LogOut, Settings } from 'lucide-react';
import { type ReactElement, useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import { authApi } from '../../lib/api-client';
import { useAuthStore } from '../../stores/auth-store';
import { useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useWorkspaceStore } from '../../stores/workspace-store';
import { useCalendarsQuery } from './api';
import type { Calendar } from './types';

function CalendarItem({ calendar }: { calendar: Calendar }): ReactElement {
  const { selectedCalendarIds, toggleCalendar } = useCalendarUiStore();
  const isVisible = selectedCalendarIds.has(calendar.id);

  return (
    <button
      type="button"
      onClick={() => toggleCalendar(calendar.id)}
      className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition-colors hover:bg-[var(--nf-color-surface-hover)]"
    >
      <span
        className="h-3 w-3 shrink-0 rounded-full"
        style={{
          backgroundColor: calendar.displayColor || calendar.color,
          opacity: isVisible ? 1 : 0.3,
        }}
      />
      <span
        className="flex-1 truncate text-[14px]"
        style={{
          color: isVisible ? 'var(--nf-color-fg)' : 'var(--nf-color-fg-subtle)',
        }}
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
      <h3
        className="px-4 pt-4 pb-1 text-[11px] font-semibold uppercase tracking-wider"
        style={{ color: 'var(--nf-color-fg-subtle)' }}
      >
        {title}
      </h3>
      {calendars.map((cal) => (
        <CalendarItem key={cal.id} calendar={cal} />
      ))}
    </div>
  );
}

export default function CalendarSidebar(): ReactElement {
  const { t } = useTranslation();
  const { data: calendars, isLoading, isError } = useCalendarsQuery();
  const clearAuth = useAuthStore((s) => s.clearAuth);
  const clearWorkspace = useWorkspaceStore((s) => s.clearWorkspace);
  const user = useAuthStore((s) => s.user);
  const toggleSettings = useCalendarUiStore((s) => s.toggleSettings);

  const handleLogout = useCallback(async () => {
    try {
      await authApi.logout();
    } catch {
      // Proceed with local cleanup even if the API call fails
    }
    clearAuth();
    clearWorkspace();
    window.location.href = '/login';
  }, [clearAuth, clearWorkspace]);

  if (isLoading) {
    return (
      <aside className="glass-surface flex h-full w-[260px] shrink-0 flex-col border-r border-[var(--nf-color-border)] p-4">
        <div className="space-y-2">
          {['sk-a', 'sk-b', 'sk-c'].map((id) => (
            <div
              key={id}
              className="h-8 animate-pulse rounded bg-[var(--nf-color-surface-hover)]"
            />
          ))}
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
    <aside className="glass-surface flex h-full w-[260px] shrink-0 flex-col border-r border-[var(--nf-color-border)]">
      <div className="flex-1 overflow-y-auto">
        {hasNoCalendars ? (
          <div className="flex flex-col items-center gap-2 px-4 py-8">
            <p className="text-sm" style={{ color: 'var(--nf-color-fg-subtle)' }}>
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
      <div className="border-t border-[var(--nf-color-border)] px-3 py-2">
        {user && (
          <p className="mb-2 truncate px-3 text-xs" style={{ color: 'var(--nf-color-fg-subtle)' }}>
            {user.email}
          </p>
        )}
        <button
          type="button"
          onClick={toggleSettings}
          className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-[var(--nf-color-fg-muted)] transition-colors hover:bg-[var(--nf-color-surface-hover)]"
        >
          <Settings className="h-4 w-4" />
          {t('settings.title')}
        </button>
        <button
          type="button"
          onClick={handleLogout}
          className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-[var(--nf-color-fg-muted)] transition-colors hover:bg-[var(--nf-color-surface-hover)]"
        >
          <LogOut className="h-4 w-4" />
          {t('auth.signOut')}
        </button>
      </div>
    </aside>
  );
}
