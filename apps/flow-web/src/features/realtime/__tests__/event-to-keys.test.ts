import { describe, expect, it } from 'vitest';
import { keysForEvent, type StreamEvent } from '../event-to-keys';

const evt = (kind: StreamEvent['kind']): StreamEvent => ({
  kind,
  workspaceId: 'ws-1',
  at: 0,
});

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

  it('resync includes calendar + item + share prefixes', () => {
    const keys = keysForEvent(evt('resync'));
    const flattened = keys.map((k) => k.join('/'));
    expect(flattened).toContain('calendars/ws-1');
    expect(flattened).toContain('events/ws-1');
    expect(flattened).toContain('public-shares/ws-1');
    expect(flattened).toContain('tasks');
  });
});
