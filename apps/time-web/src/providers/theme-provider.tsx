/**
 * App-specific ThemeProvider that delegates to the shared ThemeProvider
 * from @nodate-flow/ui and supplies time-web's server sync callbacks.
 *
 * Re-exports useThemeContext as useTheme and the shared types/constants
 * so existing imports continue to resolve.
 */

import {
  ThemeProvider as SharedThemeProvider,
  type ThemePreference,
  useThemeContext,
} from '@nodate-flow/ui/providers/theme-provider';
import type { ReactElement, ReactNode } from 'react';

import { authStore } from '../features/auth/auth-store';
import { authSdk } from '../lib/sdk';

export {
  type ThemePreference,
  type ColorMode,
  type ThemeFamily,
  type ThemeId,
  type ThemeContextValue,
  splitThemeId,
  joinThemeId,
  THEME_PREFERENCES,
} from '@nodate-flow/ui/providers/theme-provider';

async function fetchServerTheme(): Promise<ThemePreference | null> {
  const token = authStore.getState().accessToken;
  if (!token) return null;
  const res = await authSdk.GET('/auth/me');
  const me = res.data as { themePreference?: string } | undefined;
  if (me?.themePreference) return me.themePreference as ThemePreference;
  return null;
}

const AUTH_API_URL = import.meta.env.VITE_AUTH_API_BASE_URL ?? 'http://localhost:8082';

async function syncServerTheme(pref: ThemePreference): Promise<void> {
  const token = authStore.getState().accessToken;
  if (!token) return;
  await fetch(`${AUTH_API_URL}/me`, {
    method: 'PATCH',
    // biome-ignore lint/style/useNamingConvention: HTTP header
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    credentials: 'include',
    body: JSON.stringify({ themePreference: pref }),
  });
}

/** ThemeProvider wired with time-web's server sync. */
export function ThemeProvider({ children }: { children: ReactNode }): ReactElement {
  return (
    <SharedThemeProvider fetchServerTheme={fetchServerTheme} syncServerTheme={syncServerTheme}>
      {children}
    </SharedThemeProvider>
  );
}

/** Access the current theme preference and setter. Re-export of useThemeContext. */
export function useTheme() {
  return useThemeContext();
}
