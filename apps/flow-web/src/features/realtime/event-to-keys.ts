/**
 * event-to-keys — map wire-format stream events to prefixes from the
 * query-key factories owned by the features that read the changed data.
 */

import { agentsKeys } from '../ai-providers/agents-api';
import { aiInvocationsKeys } from '../ai-providers/invocations-api';
import { aiSuggestionsKeys } from '../ai-suggestions/api';
import { autoActionsKeys } from '../auto-actions/api';
import { calendarMemosKeys } from '../calendar-memos/api';
import { calendarSettingsKeys } from '../calendars/api';
import { calendarMembersKeys } from '../calendars/members-api';
import { calendarsRailKeys } from '../calendars-rail/api';
import { dashboardKeys } from '../dashboard/api';
import { calendarEventKeys, eventDetailKeys } from '../events/api';
import { favoritesKeys } from '../favorites/api';
import { importsKeys } from '../imports-exports/api';
import { inboxKeys } from '../inbox/api';
import { intakeKeys } from '../inbox/intake/api';
import { labelsKeys } from '../labels/api';
import { stateSuggestionsKeys } from '../nl-query/state-suggestions';
import { notificationKeys } from '../notifications/api';
import { pageKeys } from '../pages/api';
import { publicSharesKeys } from '../public-shares/api';
import { reactionsKeys } from '../reactions/api';
import { relationKeys } from '../relations/api';
import { remindersKeys } from '../reminders/api';
import { tasksKeys } from '../tasks/api';
import { lensesKeys } from '../tasks/lens-api';
import { timeboxKeys } from '../timeboxes/api';
import { timelineKeys } from '../timeline/api';
import { weeklyDigestKeys } from '../weekly-digest/api';

export type StreamKind =
  | 'task.changed'
  | 'ai.suggestion.changed'
  | 'ai.invocation.written'
  | 'notification.changed'
  | 'timebox.changed'
  | 'relation.changed'
  | 'lens.changed'
  | 'page.changed'
  | 'dashboard.changed'
  | 'label.changed'
  | 'reaction.changed'
  | 'favorite.changed'
  | 'intake.changed'
  | 'import.changed'
  | 'calendar.changed'
  | 'item.changed'
  | 'resync';

export interface StreamEvent {
  kind: StreamKind;
  workspaceId: string;
  at: number;
}

type QueryKeyPrefix = readonly unknown[];
type EventKind = Exclude<StreamKind, 'resync'>;
type EventKeyFactory = (workspaceId: string) => readonly QueryKeyPrefix[];

function workspaceIntakePrefix(workspaceId: string): QueryKeyPrefix {
  return intakeKeys.list(workspaceId, 'pending').slice(0, 2);
}

function workspaceMemoPrefix(workspaceId: string): QueryKeyPrefix {
  return calendarMemosKeys.list(workspaceId, '').slice(0, 2);
}

function workspaceCalendarMemberPrefix(workspaceId: string): QueryKeyPrefix {
  return calendarMembersKeys.list(workspaceId, '').slice(0, 3);
}

function workspaceCalendarLabelPrefix(workspaceId: string): QueryKeyPrefix {
  return ['calendars', 'labels', workspaceId];
}

function workspaceCalendarDetailPrefix(workspaceId: string): QueryKeyPrefix {
  return calendarSettingsKeys.detail(workspaceId, '').slice(0, 3);
}

function workspaceCalendarCountPrefix(workspaceId: string): QueryKeyPrefix {
  return calendarSettingsKeys.eventCount(workspaceId, '').slice(0, 3);
}

function calendarPrefixes(workspaceId: string): readonly QueryKeyPrefix[] {
  return [
    calendarEventKeys.all,
    calendarEventKeys.meEvents,
    calendarEventKeys.meTasks,
    eventDetailKeys.all,
    calendarsRailKeys.discoverable(workspaceId),
    workspaceCalendarDetailPrefix(workspaceId),
    workspaceCalendarCountPrefix(workspaceId),
    workspaceMemoPrefix(workspaceId),
    workspaceCalendarMemberPrefix(workspaceId),
    workspaceCalendarLabelPrefix(workspaceId),
    publicSharesKeys.list(workspaceId),
  ];
}

/** The compiler requires a factory mapping for each accepted stream kind. */
const eventKeyRegistry: Record<EventKind, EventKeyFactory> = {
  'task.changed': (ws) => [
    inboxKeys.all,
    workspaceIntakePrefix(ws),
    agentsKeys.list(ws),
    autoActionsKeys.list(ws),
    remindersKeys.list(ws),
    stateSuggestionsKeys.list(ws),
    weeklyDigestKeys.forWorkspace(ws),
    tasksKeys.all,
    timelineKeys.all,
  ],
  'ai.suggestion.changed': (ws) => [aiSuggestionsKeys.listPrefix(ws), timelineKeys.all],
  'ai.invocation.written': (ws) => [aiInvocationsKeys.list(ws)],
  'notification.changed': () => [notificationKeys.list(), notificationKeys.unreadCount()],
  'timebox.changed': (ws) => [timeboxKeys.list(ws)],
  'relation.changed': () => [relationKeys.all],
  'lens.changed': () => [lensesKeys.all],
  'page.changed': () => [pageKeys.all, timelineKeys.all],
  'dashboard.changed': (ws) => [dashboardKeys.list(ws)],
  'label.changed': () => [labelsKeys.all, tasksKeys.all],
  'reaction.changed': () => [reactionsKeys.all],
  'favorite.changed': (ws) => [favoritesKeys.list(ws)],
  'intake.changed': (ws) => [workspaceIntakePrefix(ws), tasksKeys.all, timelineKeys.all],
  'import.changed': () => [importsKeys.all],
  'calendar.changed': (ws) => [...calendarPrefixes(ws), timelineKeys.all],
  'item.changed': (ws) => [tasksKeys.all, ...calendarPrefixes(ws), timelineKeys.all],
};

function uniquePrefixes(keys: readonly QueryKeyPrefix[]): QueryKeyPrefix[] {
  const seen = new Set<string>();
  return keys.filter((key) => {
    const serialized = JSON.stringify(key);
    if (seen.has(serialized)) return false;
    seen.add(serialized);
    return true;
  });
}

/** Return prefixes from feature factories; resync combines the full registry. */
export function keysForEvent(evt: StreamEvent): readonly QueryKeyPrefix[] {
  if (evt.kind === 'resync') {
    return uniquePrefixes(
      Object.values(eventKeyRegistry).flatMap((factory) => factory(evt.workspaceId)),
    );
  }
  const factory = Object.hasOwn(eventKeyRegistry, evt.kind)
    ? eventKeyRegistry[evt.kind as EventKind]
    : undefined;
  return factory?.(evt.workspaceId) ?? [];
}
