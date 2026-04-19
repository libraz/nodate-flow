/**
 * ThemeProvider — shared, app-agnostic theme context.
 *
 * Manages theme persistence (localStorage), DOM attribute (`data-theme`),
 * system color-scheme resolution, and optional server synchronisation.
 * Each app supplies its own server callbacks via {@link ThemeProviderProps};
 * the provider itself has zero external dependencies beyond React.
 */

import {
  type ReactElement,
  type ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from 'react';

import { THEME_IDS, type ThemeId } from '../hooks/use-theme';

// Re-export ThemeId so consumers of this module can use it with splitThemeId / joinThemeId.
export type { ThemeId };

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Theme "family" — the visual design, independent of light/dark. */
export type ThemeFamily = 'aurora' | 'dotline' | 'glass';

/** Color mode preference. */
export type ColorMode = 'light' | 'dark' | 'system';

/** Combined preference stored in localStorage and on the server. */
export type ThemePreference = ThemeId | 'system';

/** All valid preference values (concrete themes + system). */
export const THEME_PREFERENCES: readonly ThemePreference[] = [...THEME_IDS, 'system'] as const;

/** Theme families available in the design system. */
export const THEME_FAMILIES: readonly ThemeFamily[] = ['aurora', 'dotline', 'glass'] as const;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Split a concrete ThemeId into family + mode. */
export function splitThemeId(id: ThemeId): { family: ThemeFamily; mode: 'light' | 'dark' } {
  const parts = id.split('-');
  const mode = parts.pop() as 'light' | 'dark';
  const family = parts.join('-') as ThemeFamily;
  return { family, mode };
}

/** Join a family + mode into a ThemeId. Returns undefined if invalid. */
export function joinThemeId(family: string, mode: 'light' | 'dark'): ThemeId | undefined {
  const candidate = `${family}-${mode}`;
  return isThemeId(candidate) ? candidate : undefined;
}

function isThemeId(value: unknown): value is ThemeId {
  return typeof value === 'string' && (THEME_IDS as readonly string[]).includes(value);
}

function isPreference(value: unknown): value is ThemePreference {
  return value === 'system' || isThemeId(value);
}

function resolveSystem(defaultFamily: ThemeFamily): ThemeId {
  if (typeof window === 'undefined') return `${defaultFamily}-dark` as ThemeId;
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  return `${defaultFamily}-${dark ? 'dark' : 'light'}` as ThemeId;
}

function resolvePreference(pref: ThemePreference, defaultFamily: ThemeFamily): ThemeId {
  return pref === 'system' ? resolveSystem(defaultFamily) : pref;
}

// ---------------------------------------------------------------------------
// Context
// ---------------------------------------------------------------------------

export interface ThemeContextValue {
  /** Raw user preference (may be 'system'). */
  preference: ThemePreference;
  /** Resolved concrete theme currently applied to the DOM. */
  resolved: ThemeId;
  /** Current theme family (derived from preference or resolved). */
  family: ThemeFamily;
  /** Current color mode preference ('light' | 'dark' | 'system'). */
  colorMode: ColorMode;
  /** Set combined preference (e.g. 'aurora-dark' or 'system'). */
  setPreference: (p: ThemePreference) => void;
  /** Set theme family, keeping current color mode. */
  setFamily: (f: ThemeFamily) => void;
  /** Set color mode, keeping current theme family. */
  setColorMode: (m: ColorMode) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

// ---------------------------------------------------------------------------
// Provider props
// ---------------------------------------------------------------------------

export interface ThemeProviderProps {
  children: ReactNode;
  /** localStorage key. Defaults to `'nf.theme'`. */
  storageKey?: string;
  /**
   * Default theme family when no preference is stored.
   * Defaults to `'aurora'`.
   */
  defaultFamily?: ThemeFamily;
  /**
   * Fetch the user's server-side theme preference after authentication.
   * Called once; return `null` if not authenticated or unavailable.
   */
  fetchServerTheme?: () => Promise<ThemePreference | null>;
  /**
   * Persist the user's theme preference to the server.
   * Fire-and-forget; errors are swallowed.
   */
  syncServerTheme?: (pref: ThemePreference) => Promise<void>;
  /**
   * Legacy localStorage keys to clear on boot.
   * For example: `['libsonare-theme', 'nt_theme', 'nt_color_mode']`.
   */
  legacyKeys?: readonly string[];
}

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

/** ThemeProvider manages theme state, localStorage, DOM, and optional server sync. */
export function ThemeProvider({
  children,
  storageKey = 'nf.theme',
  defaultFamily = 'aurora',
  fetchServerTheme,
  syncServerTheme,
  legacyKeys,
}: ThemeProviderProps): ReactElement {
  // ---- Initialisation (runs once, synchronously) ----
  const [preference, setPreferenceRaw] = useState<ThemePreference>(() => {
    // Clear legacy keys
    if (legacyKeys && typeof window !== 'undefined') {
      for (const key of legacyKeys) {
        try {
          window.localStorage.removeItem(key);
        } catch {
          /* ignore */
        }
      }
    }
    // Read from localStorage
    try {
      const stored = window.localStorage.getItem(storageKey);
      if (isPreference(stored)) return stored;
    } catch {
      /* ignore */
    }
    return 'system';
  });

  const [resolved, setResolved] = useState<ThemeId>(() =>
    resolvePreference(preference, defaultFamily),
  );
  const hydratedRef = useRef(false);

  // ---- Server hydration (one-shot) ----
  useEffect(() => {
    if (hydratedRef.current || !fetchServerTheme) return;
    hydratedRef.current = true;
    void fetchServerTheme().then((serverPref) => {
      if (serverPref && isPreference(serverPref)) {
        setPreferenceRaw(serverPref);
      }
    });
  }, [fetchServerTheme]);

  // ---- Apply preference to DOM + localStorage ----
  useEffect(() => {
    const next = resolvePreference(preference, defaultFamily);
    setResolved(next);
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', next);
    }
    try {
      window.localStorage.setItem(storageKey, preference);
    } catch {
      /* ignore */
    }
  }, [preference, defaultFamily, storageKey]);

  // ---- Server sync (fire-and-forget, skip initial hydration) ----
  const isFirstRender = useRef(true);
  useEffect(() => {
    if (isFirstRender.current) {
      isFirstRender.current = false;
      return;
    }
    if (syncServerTheme) {
      void syncServerTheme(preference).catch(() => {
        /* ignore */
      });
    }
  }, [preference, syncServerTheme]);

  // ---- System preference listener ----
  useEffect(() => {
    if (preference !== 'system' || typeof window === 'undefined') return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = (): void => {
      const next = resolveSystem(defaultFamily);
      setResolved(next);
      if (typeof document !== 'undefined') {
        document.documentElement.setAttribute('data-theme', next);
      }
    };
    mq.addEventListener('change', onChange);
    return () => {
      mq.removeEventListener('change', onChange);
    };
  }, [preference, defaultFamily]);

  // ---- Derived values ----
  const { family, mode } = splitThemeId(resolved);
  const colorMode: ColorMode = preference === 'system' ? 'system' : mode;

  // ---- Setters ----
  const setPreference = useCallback((p: ThemePreference) => {
    setPreferenceRaw(p);
  }, []);

  const setFamily = useCallback(
    (f: ThemeFamily) => {
      if (preference === 'system') {
        // Keep system mode — just remember the family for next explicit switch.
        // We resolve system to current concrete, then swap the family portion.
        const currentMode = splitThemeId(resolved).mode;
        const next = joinThemeId(f, currentMode);
        if (next) setPreferenceRaw(next);
      } else {
        const currentMode = splitThemeId(preference as ThemeId).mode;
        const next = joinThemeId(f, currentMode);
        if (next) setPreferenceRaw(next);
      }
    },
    [preference, resolved],
  );

  const setColorMode = useCallback(
    (m: ColorMode) => {
      if (m === 'system') {
        setPreferenceRaw('system');
      } else {
        const currentFamily = family;
        const next = joinThemeId(currentFamily, m);
        if (next) setPreferenceRaw(next);
      }
    },
    [family],
  );

  const value: ThemeContextValue = {
    preference,
    resolved,
    family,
    colorMode,
    setPreference,
    setFamily,
    setColorMode,
  };

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

/** Access the current theme context. Must be used within a ThemeProvider. */
export function useThemeContext(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useThemeContext must be used within <ThemeProvider>');
  return ctx;
}
