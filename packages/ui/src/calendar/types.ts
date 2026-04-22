/**
 * Shared calendar type contracts for @nodate-flow/ui/calendar.
 *
 * These types describe the presentational shape a calendar component
 * needs to render an event — detached from any particular SDK so both
 * flow-web and time-web can import them without pulling each other's
 * generated clients. Apps should map their SDK responses to these types
 * at the boundary.
 *
 * The enums below track the live MySQL schema as of R5 (unified calendar
 * + task integration). Downstream: the R5.14 follow-up drops `shared`
 * from CalendarKind; keeping it here for one release while the schema
 * migration bakes.
 */

export type CalendarKind = 'personal' | 'shared' | 'system';
export type EventKind = 'event' | 'block' | 'free' | 'milestone';
export type ShowAs = 'busy' | 'free' | 'tentative' | 'oof';
export type Visibility = 'default' | 'public' | 'private' | 'confidential';
export type SubscriptionRole = 'owner' | 'manager' | 'editor' | 'viewer';
export type Rsvp = 'pending' | 'accepted' | 'declined' | 'tentative' | 'needs_action';

/**
 * Task role attached to a calendar event. Matches `calendar_events.task_role`:
 *  - `event`     — this event is the 1:1 projection of tasks.event_on
 *  - `due`       — this event is the 1:1 projection of tasks.due_on
 *  - `scheduled` — this event is a time-block for work on the task (M:N)
 * `null` means the event has no task link.
 */
export type TaskRole = 'event' | 'due' | 'scheduled' | null;

export interface Calendar {
  id: string;
  kind: CalendarKind;
  name: string;
  color: string;
  role: SubscriptionRole;
  /** Per-user preferred overlay color; defaults to {@link Calendar.color}. */
  displayColor: string;
  /** Whether the current user has toggled this calendar visible in the grid. */
  visible: boolean;
  /**
   * For `kind==='system'` calendars, the provider identifier such as
   * `'holidays.jp'`. Undefined on personal/shared calendars.
   */
  systemSlug?: string;
}

export interface RecurrenceRule {
  freq: 'daily' | 'weekly' | 'monthly' | 'yearly';
  interval?: number;
  byDay?: string[];
  byMonthDay?: number[];
  until?: string;
  count?: number;
}

export interface CalendarEvent {
  id: string;
  calendarId: string;
  /** Optional workspace id for cross-workspace event lists (`/me/calendar-events`). */
  workspaceId?: string;
  workspaceName?: string;
  kind: EventKind;
  visibility: Visibility;
  showAs: ShowAs;
  title: string;
  allDay: boolean;
  /**
   * ISO 8601 start instant. May be empty / undefined for planning-stage
   * (undated) events introduced in R5.1. Consumers should filter out
   * undated events from date-grid views.
   */
  startAt: string;
  endAt: string;
  timezone: string;
  location?: string;
  memo?: string;
  ownerUserId?: string;
  blockLabel?: string;
  recurrenceRule?: RecurrenceRule | null;
  recurrenceExceptions?: string[];
  /** Task link role; see {@link TaskRole}. */
  taskRole?: TaskRole;
  /** Public ID of the linked task, when `taskRole != null`. */
  taskId?: string;
}

export interface CalendarMember {
  userId: string;
  displayName: string;
  avatarUrl?: string;
  memberColor: string;
  role: SubscriptionRole;
}
