/**
 * App-specific ThemeProvider that delegates to the shared ThemeProvider
 * from @nodate-flow/ui and supplies accounts-web's server sync callbacks.
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
import { sdk } from '../lib/sdk';

export {
  type ColorMode,
  joinThemeId,
  splitThemeId,
  THEME_PREFERENCES,
  type ThemeContextValue,
  type ThemeFamily,
  type ThemeId,
  type ThemePreference,
} from '@nodate-flow/ui/providers/theme-provider';

const LEGACY_KEYS = ['libsonare-theme', 'vitepress-theme-appearance'] as const;

async function fetchServerTheme(): Promise<ThemePreference | null> {
  const user = authStore.getState().user;
  if (!user) return null;
  const pref = user.themePreference as ThemePreference;
  return pref || null;
}

async function syncServerTheme(pref: ThemePreference): Promise<void> {
  const token = authStore.getState().accessToken;
  if (!token) return;
  await sdk.PATCH('/me', {
    body: { themePreference: pref },
  });
}

/** ThemeProvider wired with accounts-web's server sync. */
export function ThemeProvider({ children }: { children: ReactNode }): ReactElement {
  return (
    <SharedThemeProvider
      fetchServerTheme={fetchServerTheme}
      syncServerTheme={syncServerTheme}
      legacyKeys={LEGACY_KEYS}
    >
      {children}
    </SharedThemeProvider>
  );
}

/** Access the current theme preference and setter. Re-export of useThemeContext. */
export function useTheme() {
  return useThemeContext();
}
