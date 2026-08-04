/**
 * Shared calendar type contracts for @nodate-flow/ui/calendar.
 *
 * These types describe the presentational shape a calendar component
 * needs to render an event — detached from any particular SDK so app
 * callers (currently flow-web) can import them without pulling a
 * specific generated client. Apps should map their SDK responses to
 * these types at the boundary.
 *
 * The enums below track the live MySQL schema (unified calendar + task
 * integration). Sharing is a `calendar_members` grant, not a calendar
 * kind, so `CalendarKind` stays at `personal | system` — it says where
 * the contents come from, not who can see them.
 */

export type CalendarKind = 'personal' | 'system';
export type EventKind = 'event' | 'block' | 'free' | 'milestone';
export type ShowAs = 'busy' | 'free' | 'tentative' | 'oof';

/**
 * Whether a commitment can be moved. Matches `calendar_events.flexibility`,
 * and is deliberately separate from {@link ShowAs}: `showAs` answers "is
 * this time taken", `flexibility` answers "could it move". A meeting the
 * owner would gladly reschedule and one that cannot move are both `busy`.
 *  - `fixed`       cannot move (the default)
 *  - `negotiable`  the owner is willing to move it
 *  - `conditional` movable, but subject to something outside the event
 */
export type Flexibility = 'fixed' | 'negotiable' | 'conditional';

/**
 * The single answer a viewer is shown for a slot, derived from `showAs`
 * and `flexibility` together. See `getAvailability`.
 */
export type Availability = 'open' | 'tentative' | 'negotiable' | 'blocked';
export type Visibility = 'default' | 'public' | 'private' | 'confidential';
/**
 * A member's role on one calendar. Matches `calendar_members.role`, and is
 * an access grant rather than a display preference — the subscription that
 * carries {@link Calendar.displayColor} and {@link Calendar.visible} grants
 * nothing.
 *
 *  - `owner`   controls membership and can delete the calendar
 *  - `manager` controls membership, cannot delete the calendar
 *  - `editor`  writes events
 *  - `viewer`  reads
 */
export type SubscriptionRole = 'owner' | 'manager' | 'editor' | 'viewer';
export type Rsvp = 'pending' | 'accepted' | 'declined' | 'tentative' | 'needs_action';

/**
 * Task role attached to a calendar event. Matches `calendar_events.task_role`:
 *  - `due`       — this event is the 1:1 projection of tasks.due_on
 *  - `scheduled` — this event is a time-block for work on the task (M:N)
 * `null` means the event has no task link.
 */
export type TaskRole = 'due' | 'scheduled' | null;

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
   * `'holidays.jp'`. Undefined on personal calendars.
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
  /**
   * Whether the commitment can be moved. Optional so a caller mapping an
   * older payload does not have to invent a value; treat a missing one as
   * `fixed`, which is what the column defaults to.
   */
  flexibility?: Flexibility;
  title: string;
  allDay: boolean;
  /**
   * ISO 8601 start instant. May be empty / undefined for planning-stage
   * (undated) events. Consumers should filter out undated events
   * from date-grid views.
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
