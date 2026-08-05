/**
 * ThemeProvider — shared, app-agnostic theme context.
 *
 * Manages theme persistence (localStorage), DOM attribute (`data-theme`),
 * system color-scheme resolution, and optional server synchronisation.
 * Each app supplies its own server callbacks via {@link ThemeProviderProps};
 * the provider itself has zero external dependencies beyond React.
 */

import {
  createContext,
  type ReactElement,
  type ReactNode,
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

function resolveSystem(family: ThemeFamily): ThemeId {
  if (typeof window === 'undefined') return `${family}-dark` as ThemeId;
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  return `${family}-${dark ? 'dark' : 'light'}` as ThemeId;
}

function resolvePreference(pref: ThemePreference, family: ThemeFamily): ThemeId {
  return pref === 'system' ? resolveSystem(family) : pref;
}

function isFamily(value: unknown): value is ThemeFamily {
  return typeof value === 'string' && (THEME_FAMILIES as readonly string[]).includes(value);
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

/**
 * Delays before each server-hydration retry. The session lands shortly
 * after mount, so the useful attempts are the early ones; the tail exists
 * so a slow cold start still gets the stored theme rather than none.
 */
const SERVER_HYDRATION_DELAYS_MS = [0, 100, 250, 500, 1000, 2000, 4000] as const;

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

  /**
   * The family to use while the colour mode is `system`.
   *
   * The stored preference is a single value that is either a concrete
   * theme or the string `system`, so it has nowhere to record "follow the
   * OS, in this family". Keeping the family beside it is what lets the
   * two controls stay independent: picking a family must not silently
   * pin the colour mode, and picking `system` must not forget the family.
   */
  const [family, setFamilyRaw] = useState<ThemeFamily>(() => {
    try {
      const stored = window.localStorage.getItem(`${storageKey}.family`);
      if (isFamily(stored)) return stored;
    } catch {
      /* ignore */
    }
    return isThemeId(preference) ? splitThemeId(preference).family : defaultFamily;
  });

  const [resolved, setResolved] = useState<ThemeId>(() => resolvePreference(preference, family));
  const hydratedRef = useRef(false);

  // ---- Server hydration ----
  //
  // Runs until the server actually answers. `fetchServerTheme` reads the
  // session, which this provider mounts above and therefore before — so
  // the first call reliably comes back empty, and latching on that first
  // attempt meant the stored preference was never applied at all. The
  // account would render the default theme while the profile page,
  // reading the same server value directly, displayed the saved one.
  //
  // Attempts are bounded and the callback is a local read (store + query
  // cache), not a request.
  useEffect(() => {
    if (hydratedRef.current || !fetchServerTheme) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;

    const poll = (): void => {
      void fetchServerTheme().then((serverPref) => {
        if (cancelled || hydratedRef.current) return;
        if (serverPref && isPreference(serverPref)) {
          hydratedRef.current = true;
          setPreferenceRaw(serverPref);
          if (isThemeId(serverPref)) setFamilyRaw(splitThemeId(serverPref).family);
          return;
        }
        attempt += 1;
        if (attempt >= SERVER_HYDRATION_DELAYS_MS.length) return;
        timer = setTimeout(poll, SERVER_HYDRATION_DELAYS_MS[attempt]);
      });
    };
    poll();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [fetchServerTheme]);

  // ---- Apply preference to DOM + localStorage ----
  useEffect(() => {
    const next = resolvePreference(preference, family);
    setResolved(next);
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', next);
    }
    try {
      window.localStorage.setItem(storageKey, preference);
      window.localStorage.setItem(`${storageKey}.family`, family);
    } catch {
      /* ignore */
    }
  }, [preference, family, storageKey]);

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
      const next = resolveSystem(family);
      setResolved(next);
      if (typeof document !== 'undefined') {
        document.documentElement.setAttribute('data-theme', next);
      }
    };
    mq.addEventListener('change', onChange);
    return () => {
      mq.removeEventListener('change', onChange);
    };
  }, [preference, family]);

  // ---- Derived values ----
  //
  // Read from the preference, not from `resolved`. `resolved` is state
  // written by an effect, so it trails the preference by a commit — long
  // enough for the colour-mode control to show the previous value right
  // after a change, which is the same "control disagrees with the screen"
  // problem in miniature.
  const colorMode: ColorMode = preference === 'system' ? 'system' : splitThemeId(preference).mode;

  // ---- Setters ----
  const setPreference = useCallback((p: ThemePreference) => {
    setPreferenceRaw(p);
    // A concrete preference names a family; keep the two from drifting so
    // a later switch back to `system` resumes in the family on screen.
    if (isThemeId(p)) setFamilyRaw(splitThemeId(p).family);
  }, []);

  /**
   * Change the visual family, leaving the colour mode exactly as it was.
   *
   * When the mode is `system` it stays `system`: the previous version
   * wrote a concrete theme here, which froze the account at whatever
   * light/dark state happened to be showing, stopped it following sunset,
   * flipped the untouched colour-mode control from "System" to "Light",
   * and persisted that to the server.
   */
  const setFamily = useCallback((f: ThemeFamily) => {
    setFamilyRaw(f);
    setPreferenceRaw((prev) => {
      if (prev === 'system') return prev;
      const next = joinThemeId(f, splitThemeId(prev).mode);
      return next ?? prev;
    });
  }, []);

  const setColorMode = useCallback(
    (m: ColorMode) => {
      if (m === 'system') {
        setPreferenceRaw('system');
        return;
      }
      const next = joinThemeId(family, m);
      if (next) setPreferenceRaw(next);
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
