import { DateTime } from 'luxon';
import { create } from 'zustand';

export type CalendarView = 'month' | 'week';

interface CalendarUiState {
  selectedDate: DateTime;
  currentView: CalendarView;
  selectedCalendarIds: Set<string>;
  eventModalOpen: boolean;
  editingEventId: string | null;
  eventDetailId: string | null;
  sidebarOpen: boolean;
  prefillStartTime: string | null;

  setSelectedDate: (date: DateTime) => void;
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
}

export const useCalendarUiStore = create<CalendarUiState>((set) => ({
  selectedDate: DateTime.now(),
  currentView: 'month',
  selectedCalendarIds: new Set<string>(),
  eventModalOpen: false,
  editingEventId: null,
  eventDetailId: null,
  sidebarOpen: false,
  prefillStartTime: null,

  setSelectedDate: (date) => set({ selectedDate: date }),
  setCurrentView: (view) => set({ currentView: view }),

  goToPrevious: () =>
    set((state) => {
      const unit = state.currentView === 'week' ? 'weeks' : 'months';
      return { selectedDate: state.selectedDate.minus({ [unit]: 1 }) };
    }),
  goToNext: () =>
    set((state) => {
      const unit = state.currentView === 'week' ? 'weeks' : 'months';
      return { selectedDate: state.selectedDate.plus({ [unit]: 1 }) };
    }),

  goToPreviousMonth: () =>
    set((state) => ({ selectedDate: state.selectedDate.minus({ months: 1 }) })),
  goToNextMonth: () => set((state) => ({ selectedDate: state.selectedDate.plus({ months: 1 }) })),
  goToToday: () => set({ selectedDate: DateTime.now() }),

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
}));
