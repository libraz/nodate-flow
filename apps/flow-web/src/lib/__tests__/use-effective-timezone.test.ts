import { describe, expect, it } from 'vitest';

import { resolveEffectiveTimezone } from '../use-effective-timezone';

const workspaces = [
  { id: 'ws-1', timezone: 'Europe/Berlin' },
  { id: 'ws-2', timezone: 'America/New_York' },
];

describe('resolveEffectiveTimezone', () => {
  it('prefers the profile timezone', () => {
    expect(resolveEffectiveTimezone('Asia/Tokyo', workspaces, 'ws-1')).toBe('Asia/Tokyo');
  });

  it('falls back to the active workspace', () => {
    expect(resolveEffectiveTimezone(undefined, workspaces, 'ws-2')).toBe('America/New_York');
  });

  it('falls back to the browser when neither is set', () => {
    // Not asserting a specific zone: the point is that it resolves to
    // something rather than to empty, whatever the machine says.
    expect(resolveEffectiveTimezone(undefined, [], null)).toBeTruthy();
  });

  it('ignores a workspace that is not the active one', () => {
    expect(resolveEffectiveTimezone(undefined, workspaces, 'ws-2')).not.toBe('Europe/Berlin');
  });

  it('falls through when the active workspace has no timezone', () => {
    const partial = [{ id: 'ws-3' }];
    expect(resolveEffectiveTimezone(undefined, partial, 'ws-3')).toBeTruthy();
  });

  // The order matters beyond preference: the API resolves the same
  // chain server-side, and a client that picked differently would file
  // events on different days from the reminders about them.
  it('does not let a workspace override an explicit profile setting', () => {
    expect(resolveEffectiveTimezone('Asia/Tokyo', workspaces, 'ws-2')).toBe('Asia/Tokyo');
  });
});
