import { describe, expect, it } from 'vitest';

import { landingStateFor, TRANSITIONS_BY_STATE } from '../api';

// Truth table of NextState in apps/flow-api/internal/taskstate/state.go.
const SERVER_TABLE = {
  open: { start: 'waiting', block: 'waiting', cancel: 'cancelled', complete: 'done' },
  waiting: { submit: 'review', unblock: 'open', cancel: 'cancelled' },
  review: { block: 'waiting', complete: 'done', reopen: 'waiting', cancel: 'cancelled' },
  done: { reopen: 'waiting' },
  cancelled: { reopen: 'open' },
} as const;

describe('landingStateFor', () => {
  it('matches the server state machine for every legal transition', () => {
    for (const [from, verbs] of Object.entries(SERVER_TABLE)) {
      for (const [verb, landing] of Object.entries(verbs)) {
        expect(landingStateFor(from as keyof typeof SERVER_TABLE, verb as never)).toBe(landing);
      }
    }
  });

  it('offers exactly the transitions the server accepts', () => {
    for (const [from, verbs] of Object.entries(SERVER_TABLE)) {
      const offered = [...TRANSITIONS_BY_STATE[from as keyof typeof SERVER_TABLE]].sort();
      expect(offered).toEqual(Object.keys(verbs).sort());
    }
  });

  it('refuses a transition the server refuses', () => {
    expect(landingStateFor('done', 'cancel')).toBeNull();
    expect(landingStateFor('waiting', 'start')).toBeNull();
  });
});
