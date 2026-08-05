/**
 * `weekStart` surviving the trip from the server into the session.
 *
 * The session was built by copying `/me` field by field, and `weekStart`
 * was not on the list. Anything absent from the session reads as "never
 * set", so the profile form fell back to a locale default: Saturday was
 * saved, Sunday came back, and the next save — of any field at all —
 * wrote that Sunday over the real preference. `avatarUrl` went the same
 * way.
 *
 * The mapper is the single place that decides what a session carries, so
 * this pins its output against the response rather than against a list
 * someone has to remember to extend.
 */

import type { components } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

import { userFromMe } from '../user-from-me';

type MeBody = components['schemas']['MeBody'];

function meResponse(overrides: Partial<MeBody> = {}): MeBody {
  return {
    id: 'user-1',
    email: 'a@example.com',
    displayName: 'A',
    locale: 'ja',
    timezone: 'Asia/Tokyo',
    country: 'JP',
    themePreference: 'glass-dark',
    weekStart: 'sat',
    isInstanceAdmin: false,
    avatarUrl: 'https://example.test/avatars/user-1?v=abc',
    calendarShiftDefault: 'ask',
    notifEmailAssignment: true,
    notifEmailDigest: true,
    notifEmailDueSoon: true,
    notifEmailMention: true,
    notifWebPush: false,
    ...overrides,
  } as MeBody;
}

describe('userFromMe', () => {
  it('carries weekStart into the session', () => {
    expect(userFromMe(meResponse()).weekStart).toBe('sat');
  });

  it('carries every weekStart the server can return', () => {
    for (const value of ['mon', 'sun', 'sat'] as const) {
      expect(userFromMe(meResponse({ weekStart: value })).weekStart).toBe(value);
    }
  });

  it('keeps the avatar, which the hand-written copy also dropped', () => {
    expect(userFromMe(meResponse()).avatarUrl).toBe('https://example.test/avatars/user-1?v=abc');
  });

  it('reports the server value rather than a locale-shaped guess', () => {
    // A Japanese account saving Saturday is the case that used to break:
    // the fallback for `ja` is Monday, so the real preference vanished.
    const user = userFromMe(meResponse({ locale: 'ja', weekStart: 'sat' }));
    expect(user.weekStart).toBe('sat');
    expect(user.weekStart).not.toBe('mon');
  });
});
