import { Zone } from '@nodate-flow/ui/time';
import { describe, expect, it } from 'vitest';

import { resolveEffectiveZone } from '../use-effective-timezone';

const workspaces = [
  { id: 'ws-1', timezone: 'Europe/Berlin' },
  { id: 'ws-2', timezone: 'America/New_York' },
];

describe('resolveEffectiveZone', () => {
  it('prefers the profile timezone', () => {
    expect(resolveEffectiveZone('Asia/Tokyo', workspaces, 'ws-1').name).toBe('Asia/Tokyo');
  });

  it('falls back to the active workspace', () => {
    expect(resolveEffectiveZone(undefined, workspaces, 'ws-2').name).toBe('America/New_York');
  });

  it('falls back to the browser when neither is set', () => {
    // Not asserting a specific zone: the point is that it resolves to
    // the reader's own, whatever the machine says, rather than to UTC.
    expect(resolveEffectiveZone(undefined, [], null).name).toBe(Zone.browser().name);
  });

  it('ignores a workspace that is not the active one', () => {
    expect(resolveEffectiveZone(undefined, workspaces, 'ws-2').name).not.toBe('Europe/Berlin');
  });

  it('falls through when the active workspace has no timezone', () => {
    const partial = [{ id: 'ws-3' }];
    expect(resolveEffectiveZone(undefined, partial, 'ws-3').name).toBe(Zone.browser().name);
  });

  // The order matters beyond preference: the API resolves the same
  // chain server-side, and a client that picked differently would file
  // events on different days from the reminders about them.
  it('does not let a workspace override an explicit profile setting', () => {
    expect(resolveEffectiveZone('Asia/Tokyo', workspaces, 'ws-2').name).toBe('Asia/Tokyo');
  });

  it('skips a stored timezone the runtime does not recognise', () => {
    // A profile carrying a zone this browser has never heard of is not a
    // reason to answer UTC and silently move every day boundary; the
    // next candidate in the chain is the honest answer.
    expect(resolveEffectiveZone('Mars/Olympus_Mons', workspaces, 'ws-2').name).toBe(
      'America/New_York',
    );
  });

  it('hands back the same instance for the same zone', () => {
    // Call sites list the resolved zone in dependency arrays. A fresh
    // object per render would make every one of those fire on every
    // render, which is invisible until a grid starts rebuilding itself.
    expect(resolveEffectiveZone('Asia/Tokyo', workspaces, 'ws-1')).toBe(
      resolveEffectiveZone('Asia/Tokyo', workspaces, 'ws-2'),
    );
  });
});
