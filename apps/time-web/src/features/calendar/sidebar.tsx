import { Eye, EyeOff, LogOut } from 'lucide-react';
import { type ReactElement, useCallback } from 'react';

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
      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-gray-100"
    >
      <span
        className="h-3 w-3 shrink-0 rounded-full"
        style={{ backgroundColor: calendar.displayColor || calendar.color }}
      />
      <span className="flex-1 truncate text-left">{calendar.name}</span>
      {isVisible ? (
        <Eye className="h-4 w-4 text-gray-400" />
      ) : (
        <EyeOff className="h-4 w-4 text-gray-300" />
      )}
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
    <div className="mb-4">
      <h3 className="mb-1 px-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
        {title}
      </h3>
      {calendars.map((cal) => (
        <CalendarItem key={cal.id} calendar={cal} />
      ))}
    </div>
  );
}

export default function CalendarSidebar(): ReactElement {
  const { data: calendars, isLoading } = useCalendarsQuery();
  const clearAuth = useAuthStore((s) => s.clearAuth);
  const clearWorkspace = useWorkspaceStore((s) => s.clearWorkspace);
  const user = useAuthStore((s) => s.user);

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

  if (isLoading || !calendars) {
    return (
      <aside className="w-60 shrink-0 border-r border-gray-200 p-4">
        <div className="space-y-2">
          {['sk-a', 'sk-b', 'sk-c'].map((id) => (
            <div key={id} className="h-8 animate-pulse rounded bg-gray-100" />
          ))}
        </div>
      </aside>
    );
  }

  const shared = calendars.filter((c) => c.kind === 'shared');
  const personal = calendars.filter((c) => c.kind === 'personal');
  const system = calendars.filter((c) => c.kind === 'system');

  return (
    <aside className="flex w-60 shrink-0 flex-col overflow-y-auto border-r border-gray-200">
      <div className="flex-1 p-4">
        <CalendarSection title="Shared Calendars" calendars={shared} />
        <CalendarSection title="My Calendar" calendars={personal} />
        <CalendarSection title="Other" calendars={system} />
      </div>
      <div className="border-t border-gray-200 p-4">
        {user && <p className="mb-2 truncate text-xs text-gray-500">{user.email}</p>}
        <button
          type="button"
          onClick={handleLogout}
          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-gray-600 hover:bg-gray-100"
        >
          <LogOut className="h-4 w-4" />
          Sign out
        </button>
      </div>
    </aside>
  );
}
