import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';
import { type IntakeStatus, intakeKeys } from '../../inbox/intake/api';
import { keysForEvent, type StreamEvent } from '../event-to-keys';

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

describe('keysForEvent', () => {
  it('maps calendar.changed to calendar + event + share prefixes', () => {
    const keys = keysForEvent(evt('calendar.changed'));
    expect(keys).toEqual([
      ['calendars', 'ws-1'],
      ['events', 'ws-1'],
      ['public-shares', 'ws-1'],
    ]);
  });

  it('maps item.changed to tasks + calendars + events (both sides of link)', () => {
    const keys = keysForEvent(evt('item.changed'));
    expect(keys).toEqual([['tasks'], ['calendars', 'ws-1'], ['events', 'ws-1']]);
  });

  it('intake.changed invalidates the key the intake tab caches under', () => {
    expect(invalidatesIntakeList('intake.changed', 'pending')).toBe(true);
    expect(invalidatesIntakeList('intake.changed', 'accepted')).toBe(true);
  });

  it('resync invalidates the key the intake tab caches under', () => {
    expect(invalidatesIntakeList('resync', 'pending')).toBe(true);
  });

  it('resync includes calendar + item + share prefixes', () => {
    const keys = keysForEvent(evt('resync'));
    const flattened = keys.map((k) => k.join('/'));
    expect(flattened).toContain('calendars/ws-1');
    expect(flattened).toContain('events/ws-1');
    expect(flattened).toContain('public-shares/ws-1');
    expect(flattened).toContain('tasks');
  });
});
