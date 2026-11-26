import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';
import { aiInvocationsKeys } from '../../ai-providers/invocations-api';
import { aiSuggestionsKeys } from '../../ai-suggestions/api';
import { autoActionsKeys } from '../../auto-actions/api';
import { calendarMemosKeys } from '../../calendar-memos/api';
import { calendarSettingsKeys } from '../../calendars/api';
import { calendarMembersKeys } from '../../calendars/members-api';
import { calendarsRailKeys } from '../../calendars-rail/api';
import { dashboardKeys } from '../../dashboard/api';
import { calendarEventKeys, eventDetailKeys } from '../../events/api';
import { favoritesKeys } from '../../favorites/api';
import { importsKeys } from '../../imports-exports/api';
import { inboxKeys } from '../../inbox/api';
import { type IntakeStatus, intakeKeys } from '../../inbox/intake/api';
import { labelsKeys } from '../../labels/api';
import { stateSuggestionsKeys } from '../../nl-query/state-suggestions';
import { notificationKeys } from '../../notifications/api';
import { pageKeys } from '../../pages/api';
import { publicSharesKeys } from '../../public-shares/api';
import { reactionsKeys } from '../../reactions/api';
import { relationKeys } from '../../relations/api';
import { remindersKeys } from '../../reminders/api';
import { tasksKeys } from '../../tasks/api';
import { lensesKeys } from '../../tasks/lens-api';
import { timeboxKeys } from '../../timeboxes/api';
import { timelineKeys } from '../../timeline/api';
import { weeklyDigestKeys } from '../../weekly-digest/api';
import { keysForEvent, type StreamEvent, type StreamKind } from '../event-to-keys';

const evt = (kind: StreamEvent['kind']): StreamEvent => ({
  kind,
  workspaceId: 'ws-1',
  at: 0,
});

/**
 * Seed a client with the query key the intake tab actually caches
 * under, apply the invalidation the stream hook would apply, and report
 * whether the cached entry was marked stale. This drives the real
 * prefix-matching rather than comparing two literals, so a drift on
 * either side of the mapping shows up here.
 */
function invalidatesIntakeList(kind: StreamEvent['kind'], status: IntakeStatus): boolean {
  const qc = new QueryClient();
  const key = intakeKeys.list('ws-1', status);
  qc.setQueryData(key, []);
  for (const queryKey of keysForEvent(evt(kind))) {
    void qc.invalidateQueries({ queryKey });
  }
  return qc.getQueryState(key)?.isInvalidated === true;
}

async function invalidatedStates(
  kind: StreamEvent['kind'],
  registeredKeys: readonly (readonly unknown[])[],
): Promise<boolean[]> {
  const qc = new QueryClient();
  for (const key of registeredKeys) qc.setQueryData(key, []);
  await Promise.all(keysForEvent(evt(kind)).map((queryKey) => qc.invalidateQueries({ queryKey })));
  return registeredKeys.map((key) => qc.getQueryState(key)?.isInvalidated === true);
}

const registeredEventCaches: ReadonlyArray<{
  kind: Exclude<StreamKind, 'resync'>;
  keys: readonly (readonly unknown[])[];
}> = [
  {
    kind: 'task.changed',
    keys: [
      autoActionsKeys.list('ws-1'),
      remindersKeys.list('ws-1'),
      stateSuggestionsKeys.list('ws-1'),
      weeklyDigestKeys.forWorkspace('ws-1'),
      tasksKeys.list('project-1'),
      tasksKeys.detail('task-1'),
      timelineKeys.infinite('task', 'task-1', { limit: 2 }),
      timelineKeys.infinite('project', 'project-1', { limit: 2 }),
      timelineKeys.infinite('workspace', 'ws-1', { limit: 2 }),
    ],
  },
  {
    kind: 'ai.suggestion.changed',
    keys: [aiSuggestionsKeys.list('ws-1', 'user-1'), timelineKeys.infinite('workspace', 'ws-1')],
  },
  { kind: 'ai.invocation.written', keys: [aiInvocationsKeys.list('ws-1')] },
  {
    kind: 'notification.changed',
    keys: [notificationKeys.list(), notificationKeys.unreadCount()],
  },
  {
    kind: 'timebox.changed',
    keys: [
      timeboxKeys.list('ws-1'),
      timeboxKeys.detail('ws-1', 'timebox-1'),
      timeboxKeys.tasks('ws-1', 'timebox-1'),
    ],
  },
  {
    kind: 'relation.changed',
    keys: [relationKeys.forTask('task-1'), relationKeys.forWorkspace('ws-1')],
  },
  {
    kind: 'lens.changed',
    keys: [lensesKeys.list('ws-1'), lensesKeys.list('ws-1', 'project-1')],
  },
  {
    kind: 'page.changed',
    keys: [
      pageKeys.list('ws-1'),
      pageKeys.detail('page-1'),
      pageKeys.children('page-1'),
      pageKeys.search('ws-1', 'notes'),
      timelineKeys.infinite('workspace', 'ws-1'),
    ],
  },
  { kind: 'dashboard.changed', keys: [dashboardKeys.list('ws-1')] },
  {
    kind: 'label.changed',
    keys: [labelsKeys.list('ws-1'), labelsKeys.forTask('task-1'), tasksKeys.detail('task-1')],
  },
  { kind: 'reaction.changed', keys: [reactionsKeys.forTask('task-1')] },
  { kind: 'favorite.changed', keys: [favoritesKeys.list('ws-1')] },
  {
    kind: 'intake.changed',
    keys: [intakeKeys.list('ws-1', 'pending'), intakeKeys.list('ws-1', 'accepted')],
  },
  { kind: 'import.changed', keys: [importsKeys.list('ws-1')] },
  {
    kind: 'calendar.changed',
    keys: [
      calendarEventKeys.calendars('ws-1'),
      calendarEventKeys.list('ws-1', 'calendar-1'),
      calendarEventKeys.meEvents,
      calendarEventKeys.meTasks,
      eventDetailKeys.detail('ws-1', 'calendar-1', 'event-1'),
      calendarMemosKeys.list('ws-1', 'calendar-1'),
      calendarMembersKeys.list('ws-1', 'calendar-1'),
      ['calendars', 'labels', 'ws-1', 'calendar-1'],
      calendarSettingsKeys.detail('ws-1', 'calendar-1'),
      calendarSettingsKeys.eventCount('ws-1', 'calendar-1'),
      calendarsRailKeys.discoverable('ws-1'),
      publicSharesKeys.list('ws-1'),
      timelineKeys.infinite('workspace', 'ws-1'),
    ],
  },
  {
    kind: 'item.changed',
    keys: [
      tasksKeys.list('project-1'),
      calendarEventKeys.calendars('ws-1'),
      calendarEventKeys.list('ws-1', 'calendar-1'),
      eventDetailKeys.detail('ws-1', 'calendar-1', 'event-1'),
      timelineKeys.infinite('workspace', 'ws-1'),
    ],
  },
];

describe('keysForEvent', () => {
  it('calendar.changed invalidates registered calendar event and detail queries', async () => {
    const registeredKeys = [
      calendarEventKeys.calendars('ws-1'),
      calendarEventKeys.list('ws-1', 'cal-1'),
      calendarEventKeys.meEvents,
      calendarEventKeys.meTasks,
      eventDetailKeys.detail('ws-1', 'cal-1', 'evt-1'),
      calendarMemosKeys.list('ws-1', 'cal-1'),
      calendarMembersKeys.list('ws-1', 'cal-1'),
      ['calendars', 'labels', 'ws-1', 'cal-1'],
      calendarSettingsKeys.detail('ws-1', 'cal-1'),
      calendarSettingsKeys.eventCount('ws-1', 'cal-1'),
      calendarsRailKeys.discoverable('ws-1'),
      publicSharesKeys.list('ws-1'),
    ];
    expect(await invalidatedStates('calendar.changed', registeredKeys)).toEqual([
      true,
      true,
      true,
      true,
      true,
      true,
      true,
      true,
      true,
      true,
      true,
      true,
    ]);
  });

  it('item.changed includes the task and calendar-event factory roots', () => {
    const keys = keysForEvent(evt('item.changed'));
    expect(keys).toContainEqual(tasksKeys.all);
    expect(keys).toContainEqual(calendarEventKeys.all);
    expect(keys).toContainEqual(eventDetailKeys.all);
  });

  it('ignores an event kind from a newer server version', () => {
    for (const kind of ['future.changed', 'constructor']) {
      const futureEvent = { ...evt('item.changed'), kind } as unknown as StreamEvent;
      expect(keysForEvent(futureEvent)).toEqual([]);
    }
  });

  it('intake.changed invalidates the key the intake tab caches under', () => {
    expect(invalidatesIntakeList('intake.changed', 'pending')).toBe(true);
    expect(invalidatesIntakeList('intake.changed', 'accepted')).toBe(true);
  });

  it('resync invalidates the key the intake tab caches under', () => {
    expect(invalidatesIntakeList('resync', 'pending')).toBe(true);
  });

  it('page.changed invalidates registered list, detail, children, and search caches', async () => {
    const registeredKeys = [
      pageKeys.list('ws-1'),
      pageKeys.detail('page-1'),
      pageKeys.children('page-1'),
      pageKeys.search('ws-1', 'notes'),
    ];
    expect(await invalidatedStates('page.changed', registeredKeys)).toEqual([
      true,
      true,
      true,
      true,
    ]);
  });

  it('task.changed and resync invalidate every loaded timeline scope', async () => {
    const registeredKeys = [
      timelineKeys.infinite('task', 'task-1', { limit: 2 }),
      timelineKeys.infinite('project', 'project-1', { limit: 2 }),
      timelineKeys.infinite('workspace', 'ws-1', { limit: 2 }),
    ];
    expect(await invalidatedStates('task.changed', registeredKeys)).toEqual([true, true, true]);
    expect(await invalidatedStates('resync', registeredKeys)).toEqual([true, true, true]);
  });

  it('invalidates registered cache keys for every stream event family', async () => {
    for (const { kind, keys } of registeredEventCaches) {
      expect(await invalidatedStates(kind, keys), kind).toEqual(keys.map(() => true));
    }
  });

  it('resync reaches every registered workspace cache family', async () => {
    const registeredKeys = registeredEventCaches.flatMap(({ keys }) => keys);
    expect(await invalidatedStates('resync', registeredKeys)).toEqual(
      registeredKeys.map(() => true),
    );
  });
});

it('refreshes Inbox and all intake tabs on a compatible workflow hint', async () => {
  expect(
    await invalidatedStates('task.changed', [
      inboxKeys.list(),
      ...(['pending', 'accepted', 'rejected', 'snoozed', 'duplicate'] as const).map((status) =>
        intakeKeys.list('ws-1', status),
      ),
    ]),
  ).toEqual([true, true, true, true, true, true]);
});
