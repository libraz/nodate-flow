/**
 * Calendar UI slice (Zustand). Controls navigation, view state, modals,
 * panels, and mobile tab for the calendar app. Uses vanilla createStore so
 * non-React code can access state when needed.
 *
 * Theme management has been moved to the shared ThemeProvider from
 * @nodate-flow/ui (see providers/theme-provider.tsx).
 */

import { DateTime } from 'luxon';
import { useStore } from 'zustand';
import { createStore } from 'zustand/vanilla';

export type CalendarView = 'month' | 'week';
export type RightPanel = 'memo' | 'members' | 'share' | 'notifications';
export type MobileTab = 'calendar' | 'memo' | 'search' | 'settings';

export interface CalendarUiState {
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
}

/**
 * Vanilla store, exported so non-React code can read calendar UI state.
 */
export const calendarUiStore = createStore<CalendarUiState>((set) => ({
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
}));

/** React hook with selector. Always pass a selector to avoid over-rendering. */
export function useCalendarUi<T>(selector: (state: CalendarUiState) => T): T {
  return useStore(calendarUiStore, selector);
}
