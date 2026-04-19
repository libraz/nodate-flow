/**
 * Theme utilities for time-web.
 *
 * Uses the shared `nf.theme` localStorage key so that theme preference
 * is synchronized with flow-web and other apps when the user is logged in.
 */

export type Theme = 'glass' | 'aurora' | 'dotline';
export type ColorMode = 'light' | 'dark' | 'system';

/** Combined preference stored in localStorage (e.g. 'aurora-dark' or 'system'). */
export type ThemePreference = `${Theme}-${'light' | 'dark'}` | 'system';

const STORAGE_KEY = 'nf.theme';

function resolveSystemColorScheme(): 'light' | 'dark' {
  if (typeof window === 'undefined') return 'light';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function resolveEffectiveMode(mode: ColorMode): 'light' | 'dark' {
  return mode === 'system' ? resolveSystemColorScheme() : mode;
}

/** Apply the given theme and color mode to the document root element. */
export function applyTheme(theme: Theme, colorMode: ColorMode): void {
  if (typeof document === 'undefined') return;
  const effective = resolveEffectiveMode(colorMode);
  const themeId = `${theme}-${effective}`;
  document.documentElement.setAttribute('data-theme', themeId);
}

/** Toggle the `dark` class on `<html>` based on color mode, respecting system preference. */
export function applyColorMode(mode: ColorMode): void {
  if (typeof document === 'undefined') return;
  const effective = resolveEffectiveMode(mode);
  document.documentElement.classList.toggle('dark', effective === 'dark');
}

/** Listen to system color scheme changes. Returns a cleanup function. */
export function watchSystemColorScheme(callback: (isDark: boolean) => void): () => void {
  if (typeof window === 'undefined') return () => {};
  const mql = window.matchMedia('(prefers-color-scheme: dark)');
  const handler = (e: MediaQueryListEvent) => callback(e.matches);
  mql.addEventListener('change', handler);
  return () => mql.removeEventListener('change', handler);
}

/** Split a stored preference into theme + colorMode. */
export function splitPreference(pref: string): { theme: Theme; colorMode: ColorMode } {
  if (pref === 'system') return { theme: 'glass', colorMode: 'system' };
  const match = pref.match(/^(glass|aurora|dotline)-(light|dark)$/);
  if (match) return { theme: match[1] as Theme, colorMode: match[2] as ColorMode };
  return { theme: 'glass', colorMode: 'system' };
}

/** Combine theme + colorMode into a preference string. */
export function combinePreference(theme: Theme, colorMode: ColorMode): ThemePreference {
  if (colorMode === 'system') return 'system';
  return `${theme}-${colorMode}`;
}

/** Read persisted theme + colorMode from the shared localStorage key. */
export function loadPreference(): { theme: Theme; colorMode: ColorMode } {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) return splitPreference(stored);
  } catch {
    // ignore
  }
  return { theme: 'glass', colorMode: 'system' };
}

/** Persist theme preference to the shared localStorage key. */
export function savePreference(theme: Theme, colorMode: ColorMode): void {
  try {
    localStorage.setItem(STORAGE_KEY, combinePreference(theme, colorMode));
  } catch {
    // ignore
  }
}
