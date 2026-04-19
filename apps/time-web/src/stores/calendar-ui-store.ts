import { DateTime } from 'luxon';
import { create } from 'zustand';

import {
  type ColorMode,
  type Theme,
  applyColorMode,
  applyTheme,
  combinePreference,
  loadPreference,
  savePreference,
} from '../lib/theme';
import { useAuthStore } from './auth-store';

const AUTH_API_URL = import.meta.env.VITE_AUTH_API_BASE_URL ?? 'http://localhost:8082';

/** Fire-and-forget server sync of theme preference. */
function syncThemeToServer(theme: Theme, colorMode: ColorMode): void {
  const token = useAuthStore.getState().accessToken;
  if (!token) return;
  const pref = combinePreference(theme, colorMode);
  void fetch(`${AUTH_API_URL}/me`, {
    method: 'PATCH',
    // biome-ignore lint/style/useNamingConvention: HTTP header
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    credentials: 'include',
    body: JSON.stringify({ themePreference: pref }),
  }).catch(() => {
    /* ignore — local state is the fast path */
  });
}

export type CalendarView = 'month' | 'week';
export type RightPanel = 'memo' | 'members' | 'share' | 'notifications';
export type MobileTab = 'calendar' | 'memo' | 'search' | 'settings';

interface CalendarUiState {
  selectedDate: DateTime;
  displayMonth: DateTime;
  currentView: CalendarView;
  selectedCalendarIds: Set<string>;
  eventModalOpen: boolean;
  editingEventId: string | null;
  eventDetailId: string | null;
  sidebarOpen: boolean;
  prefillStartTime: string | null;
  rightPanel: RightPanel | null;
  showSearch: boolean;
  searchQuery: string;
  leftSidebarExpanded: boolean;
  showDayDetail: boolean;
  theme: Theme;
  colorMode: ColorMode;
  showSettings: boolean;
  mobileTab: MobileTab;

  setSelectedDate: (date: DateTime) => void;
  setDisplayMonth: (date: DateTime) => void;
  setCurrentView: (view: CalendarView) => void;
  goToPrevious: () => void;
  goToNext: () => void;
  goToPreviousMonth: () => void;
  goToNextMonth: () => void;
  goToToday: () => void;
  toggleCalendar: (calendarId: string) => void;
  setAllCalendars: (ids: string[]) => void;
  openEventModal: (eventId?: string, prefillStart?: string) => void;
  closeEventModal: () => void;
  openEventDetail: (eventId: string) => void;
  closeEventDetail: () => void;
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  setRightPanel: (panel: RightPanel | null) => void;
  toggleSearch: () => void;
  setSearchQuery: (q: string) => void;
  toggleLeftSidebar: () => void;
  openDayDetail: () => void;
  closeDayDetail: () => void;
  toggleSettings: () => void;
  setMobileTab: (tab: MobileTab) => void;
  setTheme: (theme: Theme) => void;
  setColorMode: (mode: ColorMode) => void;
}

export const useCalendarUiStore = create<CalendarUiState>((set) => ({
  selectedDate: DateTime.now(),
  displayMonth: DateTime.now().startOf('month'),
  currentView: 'month',
  selectedCalendarIds: new Set<string>(),
  eventModalOpen: false,
  editingEventId: null,
  eventDetailId: null,
  sidebarOpen: false,
  prefillStartTime: null,
  rightPanel: null,
  showSearch: false,
  searchQuery: '',
  leftSidebarExpanded: true,
  showDayDetail: false,
  theme: loadPreference().theme,
  colorMode: loadPreference().colorMode,
  showSettings: false,
  mobileTab: 'calendar',

  setSelectedDate: (date) => set({ selectedDate: date }),
  setDisplayMonth: (date) => set({ displayMonth: date }),
  setCurrentView: (view) => set({ currentView: view }),

  goToPrevious: () =>
    set((state) => {
      if (state.currentView === 'week') {
        return { selectedDate: state.selectedDate.minus({ weeks: 1 }) };
      }
      return { displayMonth: state.displayMonth.minus({ months: 1 }) };
    }),
  goToNext: () =>
    set((state) => {
      if (state.currentView === 'week') {
        return { selectedDate: state.selectedDate.plus({ weeks: 1 }) };
      }
      return { displayMonth: state.displayMonth.plus({ months: 1 }) };
    }),

  goToPreviousMonth: () =>
    set((state) => ({ displayMonth: state.displayMonth.minus({ months: 1 }) })),
  goToNextMonth: () => set((state) => ({ displayMonth: state.displayMonth.plus({ months: 1 }) })),
  goToToday: () =>
    set({ selectedDate: DateTime.now(), displayMonth: DateTime.now().startOf('month') }),

  toggleCalendar: (calendarId) =>
    set((state) => {
      const next = new Set(state.selectedCalendarIds);
      if (next.has(calendarId)) {
        next.delete(calendarId);
      } else {
        next.add(calendarId);
      }
      return { selectedCalendarIds: next };
    }),

  setAllCalendars: (ids) => set({ selectedCalendarIds: new Set(ids) }),

  openEventModal: (eventId, prefillStart) =>
    set({
      eventModalOpen: true,
      editingEventId: eventId ?? null,
      eventDetailId: null,
      prefillStartTime: prefillStart ?? null,
    }),
  closeEventModal: () =>
    set({ eventModalOpen: false, editingEventId: null, prefillStartTime: null }),

  openEventDetail: (eventId) => set({ eventDetailId: eventId }),
  closeEventDetail: () => set({ eventDetailId: null }),

  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
  setSidebarOpen: (open) => set({ sidebarOpen: open }),

  setRightPanel: (panel) =>
    set((state) => ({
      rightPanel: state.rightPanel === panel ? null : panel,
    })),
  toggleSearch: () =>
    set((state) => ({
      showSearch: !state.showSearch,
      searchQuery: state.showSearch ? '' : state.searchQuery,
    })),
  setSearchQuery: (q) => set({ searchQuery: q }),
  toggleLeftSidebar: () => set((state) => ({ leftSidebarExpanded: !state.leftSidebarExpanded })),
  openDayDetail: () => set({ showDayDetail: true }),
  closeDayDetail: () => set({ showDayDetail: false }),

  toggleSettings: () => set((state) => ({ showSettings: !state.showSettings })),
  setMobileTab: (tab) => set({ mobileTab: tab }),
  setTheme: (theme) => {
    set((state) => {
      savePreference(theme, state.colorMode);
      applyTheme(theme, state.colorMode);
      syncThemeToServer(theme, state.colorMode);
      return { theme };
    });
  },
  setColorMode: (mode) => {
    set((state) => {
      savePreference(state.theme, mode);
      applyTheme(state.theme, mode);
      applyColorMode(mode);
      syncThemeToServer(state.theme, mode);
      return { colorMode: mode };
    });
  },
}));
