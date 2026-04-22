/**
 * Verify that the auth store correctly handles the full AuthUser shape
 * including the themePreference and isInstanceAdmin fields added in C4.
 */

import { afterEach, describe, expect, it } from 'vitest';

import { type AuthUser, authStore, selectIsAuthenticated, selectUser } from '../auth-store';

describe('authStore', () => {
  afterEach(() => {
    authStore.getState().clearSession();
  });

  const fullUser: AuthUser = {
    id: 'usr_abc123',
    email: 'test@example.com',
    displayName: 'Test User',
    locale: 'en',
    timezone: 'UTC',
    country: '',
    themePreference: 'aurora-dark',
    isInstanceAdmin: false,
  };

  it('stores themePreference via setSession', () => {
    authStore.getState().setSession('tok_1', fullUser);
    const user = authStore.getState().user;
    expect(user).not.toBeNull();
    expect(user?.themePreference).toBe('aurora-dark');
  });

  it('stores isInstanceAdmin via setSession', () => {
    authStore.getState().setSession('tok_1', { ...fullUser, isInstanceAdmin: true });
    const user = authStore.getState().user;
    expect(user).not.toBeNull();
    expect(user?.isInstanceAdmin).toBe(true);
  });

  it('preserves all AuthUser fields through setSession round-trip', () => {
    authStore.getState().setSession('tok_1', fullUser);
    const stored = authStore.getState().user;
    expect(stored).toEqual(fullUser);
  });

  it('setAccessToken updates token without touching user', () => {
    authStore.getState().setSession('tok_1', fullUser);
    authStore.getState().setAccessToken('tok_2');
    expect(authStore.getState().accessToken).toBe('tok_2');
    expect(authStore.getState().user).toEqual(fullUser);
  });

  it('clearSession nullifies both token and user', () => {
    authStore.getState().setSession('tok_1', fullUser);
    authStore.getState().clearSession();
    expect(authStore.getState().accessToken).toBeNull();
    expect(authStore.getState().user).toBeNull();
  });

  it('selectUser returns the stored user', () => {
    authStore.getState().setSession('tok_1', fullUser);
    expect(selectUser(authStore.getState())).toEqual(fullUser);
  });

  it('selectIsAuthenticated reflects token presence', () => {
    expect(selectIsAuthenticated(authStore.getState())).toBe(false);
    authStore.getState().setSession('tok_1', fullUser);
    expect(selectIsAuthenticated(authStore.getState())).toBe(true);
  });

  it('handles system theme preference', () => {
    authStore.getState().setSession('tok_1', { ...fullUser, themePreference: 'system' });
    expect(authStore.getState().user?.themePreference).toBe('system');
  });

  it('handles empty themePreference', () => {
    authStore.getState().setSession('tok_1', { ...fullUser, themePreference: '' });
    expect(authStore.getState().user?.themePreference).toBe('');
  });
});
