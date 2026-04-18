export type Theme = 'glass' | 'aurora' | 'dotline';
export type ColorMode = 'light' | 'dark' | 'system';

const THEME_STORAGE_KEY = 'nt_theme';
const COLOR_MODE_STORAGE_KEY = 'nt_color_mode';

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

/** Read persisted theme from localStorage. */
export function loadTheme(): Theme {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === 'glass' || stored === 'aurora' || stored === 'dotline') return stored;
  } catch {
    // ignore
  }
  return 'glass';
}

/** Read persisted color mode from localStorage. */
export function loadColorMode(): ColorMode {
  try {
    const stored = localStorage.getItem(COLOR_MODE_STORAGE_KEY);
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored;
  } catch {
    // ignore
  }
  return 'system';
}

/** Persist theme to localStorage. */
export function saveTheme(theme: Theme): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // ignore
  }
}

/** Persist color mode to localStorage. */
export function saveColorMode(mode: ColorMode): void {
  try {
    localStorage.setItem(COLOR_MODE_STORAGE_KEY, mode);
  } catch {
    // ignore
  }
}
