/**
 * @nodate-flow/ui/calendar — shared calendar primitives.
 *
 * Store-free, SDK-free helpers usable from any web app that renders
 * calendar events. Consumers map their SDK payloads onto
 * {@link CalendarEvent} at the boundary, then rely on the shared
 * recurrence expander and event-style helper for visual parity.
 *
 * Visual components (grid, day cell, week view) live in consumer apps
 * for now because they still couple to app-specific zustand stores and
 * react-query hooks; they will land here once the flow-web rewrite
 * settles on a common shape.
 */

export { getEventStyle } from './event-styles';
export { expandAllRecurrences, expandRecurrence } from './recurrence';

export type {
  Calendar,
  CalendarEvent,
  CalendarKind,
  CalendarMember,
  EventKind,
  RecurrenceRule,
  Rsvp,
  ShowAs,
  SubscriptionRole,
  TaskRole,
  Visibility,
} from './types';
