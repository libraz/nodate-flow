export type CalendarKind = 'personal' | 'shared' | 'system';
export type EventKind = 'event' | 'block' | 'free';
export type ShowAs = 'busy' | 'free' | 'tentative' | 'oof';
export type Visibility = 'default' | 'public' | 'private' | 'confidential';
export type SubscriptionRole = 'owner' | 'manager' | 'editor' | 'viewer';
export type Rsvp = 'pending' | 'accepted' | 'declined' | 'tentative';

export interface Calendar {
  id: string;
  kind: CalendarKind;
  name: string;
  color: string;
  role: SubscriptionRole;
  memberColor: string;
  displayColor: string;
  visible: boolean;
  /**
   * For kind==='system' calendars, the provider identifier such as
   * 'holidays.jp'. Undefined on personal/shared calendars.
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
  kind: EventKind;
  visibility: Visibility;
  showAs: ShowAs;
  title: string;
  allDay: boolean;
  startAt: string;
  endAt: string;
  timezone: string;
  location?: string;
  memo?: string;
  ownerUserId: string;
  blockLabel?: string;
  recurrenceRule?: RecurrenceRule | null;
  recurrenceExceptions?: string[];
}

export interface CalendarMember {
  userId: string;
  displayName: string;
  avatarUrl?: string;
  memberColor: string;
  role: SubscriptionRole;
}
