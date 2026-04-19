/**
 * useTheme — read and write the active design-system theme.
 *
 * The theme is persisted on `<html data-theme="...">` so that CSS variable
 * cascades take effect immediately, with no flash. Persistence to localStorage
 * is opt-in via {@link UseThemeOptions.storageKey}.
 */

import { useCallback, useEffect, useState } from 'react';

export type ThemeId =
  | 'aurora-light'
  | 'aurora-dark'
  | 'dotline-light'
  | 'dotline-dark'
  | 'glass-light'
  | 'glass-dark';

export const THEME_IDS: readonly ThemeId[] = [
  'aurora-light',
  'aurora-dark',
  'dotline-light',
  'dotline-dark',
  'glass-light',
  'glass-dark',
] as const;

export interface UseThemeOptions {
  /** Initial theme to apply if none is currently set. */
  defaultTheme?: ThemeId;
  /** If provided, the resolved theme is persisted under this localStorage key. */
  storageKey?: string;
}

export interface UseThemeResult {
  theme: ThemeId;
  setTheme: (next: ThemeId) => void;
}

function isThemeId(value: unknown): value is ThemeId {
  return typeof value === 'string' && (THEME_IDS as readonly string[]).includes(value);
}

function readInitial(options: UseThemeOptions): ThemeId {
  if (typeof document === 'undefined') {
    return options.defaultTheme ?? 'aurora-light';
  }
  const fromAttr = document.documentElement.getAttribute('data-theme');
  if (isThemeId(fromAttr)) return fromAttr;
  if (options.storageKey) {
    try {
      const stored = window.localStorage.getItem(options.storageKey);
      if (isThemeId(stored)) return stored;
    } catch {
      /* localStorage may be unavailable */
    }
  }
  return options.defaultTheme ?? 'aurora-light';
}

/**
 * useTheme returns the active theme id and a setter that updates both the
 * `<html data-theme>` attribute and (optionally) localStorage.
 */
export function useTheme(options: UseThemeOptions = {}): UseThemeResult {
  const [theme, setThemeState] = useState<ThemeId>(() => readInitial(options));

  useEffect(() => {
    if (typeof document === 'undefined') return;
    document.documentElement.setAttribute('data-theme', theme);
    if (options.storageKey) {
      try {
        window.localStorage.setItem(options.storageKey, theme);
      } catch {
        /* ignore */
      }
    }
  }, [theme, options.storageKey]);

  const setTheme = useCallback((next: ThemeId) => {
    setThemeState(next);
  }, []);

  return { theme, setTheme };
}
