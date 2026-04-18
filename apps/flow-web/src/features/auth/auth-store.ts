/**
 * Auth slice (Zustand). Holds the in-memory access token and the
 * authenticated user profile. The access token is intentionally NOT
 * persisted to localStorage; on a fresh page load we re-establish the
 * session via the httpOnly nf_rt refresh cookie (see use-auth-bootstrap).
 */

import { useStore } from 'zustand';
import { createStore } from 'zustand/vanilla';

/** Authenticated user profile mirroring MeOutputBody. */
export interface AuthUser {
  id: string;
  email: string;
  displayName: string;
  locale: string;
}

export interface AuthState {
  accessToken: string | null;
  user: AuthUser | null;
  setSession: (token: string, user: AuthUser) => void;
  setAccessToken: (token: string) => void;
  clearSession: () => void;
}

/**
 * Vanilla store, exported so non-React modules (e.g. the SDK middleware
 * in `lib/sdk.ts`) can read the access token without going through React.
 */
export const authStore = createStore<AuthState>((set) => ({
  accessToken: null,
  user: null,
  setSession: (token, user) => {
    set({ accessToken: token, user });
  },
  setAccessToken: (token) => {
    set({ accessToken: token });
  },
  clearSession: () => {
    set({ accessToken: null, user: null });
  },
}));

/** React hook with selector. Always pass a selector to avoid over-rendering. */
export function useAuth<T>(selector: (state: AuthState) => T): T {
  return useStore(authStore, selector);
}

/** Convenience selectors. */
export const selectAccessToken = (s: AuthState): string | null => s.accessToken;
export const selectUser = (s: AuthState): AuthUser | null => s.user;
export const selectIsAuthenticated = (s: AuthState): boolean => s.accessToken !== null;
