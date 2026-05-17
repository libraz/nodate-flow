/**
 * App-specific ThemeProvider that delegates to the shared ThemeProvider
 * from @nodate-flow/ui and supplies flow-web's server sync callbacks.
 *
 * Re-exports useThemeContext as useTheme and the shared types/constants
 * so existing imports continue to resolve.
 */

import {
  ThemeProvider as SharedThemeProvider,
  THEME_PREFERENCES,
  type ThemePreference,
  useThemeContext,
} from '@nodate-flow/ui/providers/theme-provider';
import type { ReactElement, ReactNode } from 'react';

import { authStore } from '../features/auth/auth-store';
import type { Me } from '../features/settings/api';
import { authSdk } from '../lib/sdk';
import { queryClient } from './query-client';

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

/** Backwards-compatible alias for ThemeId. */
export type { ThemeId as ConcreteTheme } from '@nodate-flow/ui/providers/theme-provider';

/**
 * Legacy exports for backwards compatibility with flow-web consumers
 * that import concreteThemes and themePreferences from this module.
 */
import { THEME_IDS } from '@nodate-flow/ui/hooks/use-theme';
export const concreteThemes = THEME_IDS;
export const themePreferences = THEME_PREFERENCES;

const LEGACY_KEYS = ['libsonare-theme', 'vitepress-theme-appearance'] as const;

async function fetchServerTheme(): Promise<ThemePreference | null> {
  const token = authStore.getState().accessToken;
  if (!token) return null;
  const me = queryClient.getQueryData<Me>(['me']);
  if (me?.themePreference) return me.themePreference as ThemePreference;
  return null;
}

async function syncServerTheme(pref: ThemePreference): Promise<void> {
  const token = authStore.getState().accessToken;
  if (!token) return;
  const { data } = await authSdk.PATCH('/me', { body: { themePreference: pref } });
  if (data) queryClient.setQueryData<Me>(['me'], data);
}

/** ThemeProvider wired with flow-web's server sync. */
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
