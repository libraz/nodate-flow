import { describe, expect, it } from 'vitest';

import { userFromMe, type MeResponse } from '../user-from-me';

function me(overrides: Partial<MeResponse> = {}): MeResponse {
  return {
    id: 'user-1',
    email: 'user@example.com',
    displayName: 'User One',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    avatarUrl: null,
    ...overrides,
  } as MeResponse;
}

describe('userFromMe', () => {
  it('preserves avatarUrl when present', () => {
    expect(userFromMe(me({ avatarUrl: 'https://cdn.example/avatar.png' })).avatarUrl).toBe(
      'https://cdn.example/avatar.png',
    );
  });

  it('normalizes missing avatarUrl to null', () => {
    const body = me();
    delete (body as { avatarUrl?: string | null }).avatarUrl;
    expect(userFromMe(body)).toHaveProperty('avatarUrl', null);
  });
});
