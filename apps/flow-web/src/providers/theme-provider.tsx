import { useQueryClient } from '@tanstack/react-query';
import {
  type ReactElement,
  type ReactNode,
  createContext,
  useContext,
  useEffect,
  useState,
} from 'react';

import { authStore } from '../features/auth/auth-store';
import type { Me } from '../features/settings/api';
import { sdk } from '../lib/sdk';

/** Concrete theme names (no `system`). */
export const concreteThemes = [
  'aurora-light',
  'aurora-dark',
  'dotline-light',
  'dotline-dark',
  'glass-light',
  'glass-dark',
] as const;

/** Concrete theme name. */
export type ConcreteTheme = (typeof concreteThemes)[number];

/** User-selectable theme preference (concrete or `system`). */
export type ThemePreference = ConcreteTheme | 'system';

/** All preference values, for UI switchers. */
export const themePreferences = [...concreteThemes, 'system'] as const;

interface ThemeContextValue {
  preference: ThemePreference;
  resolved: ConcreteTheme;
  setPreference: (p: ThemePreference) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);
const storageKey = 'nf.theme';

/**
 * Legacy / foreign theme keys that may exist in localStorage from prior
 * implementations or sibling tools sharing the dev origin. We proactively
 * clear them on boot so only `nf.theme` is authoritative.
 */
const legacyThemeKeys = ['libsonare-theme', 'vitepress-theme-appearance'] as const;

function clearLegacyThemeKeys(): void {
  if (typeof window === 'undefined') return;
  for (const key of legacyThemeKeys) {
    try {
      window.localStorage.removeItem(key);
    } catch {
      // ignore
    }
  }
}

function readStored(): ThemePreference {
  clearLegacyThemeKeys();
  try {
    const v = localStorage.getItem(storageKey);
    if (v === 'system') return 'system';
    if (v && (concreteThemes as readonly string[]).includes(v)) {
      return v as ConcreteTheme;
    }
  } catch {
    // ignore
  }
  return 'system';
}

function resolveSystem(): ConcreteTheme {
  if (typeof window === 'undefined') return 'aurora-dark';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'aurora-dark' : 'aurora-light';
}

function resolve(pref: ThemePreference): ConcreteTheme {
  return pref === 'system' ? resolveSystem() : pref;
}

/**
 * ThemeProvider persists theme preference to localStorage and applies
 * `data-theme` on `document.documentElement`. DB persistence is deferred
 * to F4.
 */
export function ThemeProvider({ children }: { children: ReactNode }): ReactElement {
  const qc = useQueryClient();
  const [preference, setPreferenceState] = useState<ThemePreference>(() => readStored());
  const [resolved, setResolved] = useState<ConcreteTheme>(() => resolve(readStored()));
  const [hydratedFromMe, setHydratedFromMe] = useState(false);

  // After auth bootstrap populates the `me` cache, adopt the server-side
  // preference (one-shot). We deliberately use a non-Suspense cache read so
  // ThemeProvider can mount before the user is signed in.
  useEffect(() => {
    if (hydratedFromMe) return;
    const tryHydrate = (): void => {
      if (hydratedFromMe) return;
      const token = authStore.getState().accessToken;
      if (!token) return;
      const me = qc.getQueryData<Me>(['me']);
      if (!me) return;
      setHydratedFromMe(true);
      setPreferenceState((prev) => (prev === me.themePreference ? prev : me.themePreference));
    };
    tryHydrate();
    const unsubAuth = authStore.subscribe(tryHydrate);
    const unsubCache = qc.getQueryCache().subscribe((event) => {
      if (
        event.type === 'updated' &&
        Array.isArray(event.query.queryKey) &&
        event.query.queryKey[0] === 'me'
      ) {
        // Defer to a microtask so we never call setState synchronously
        // during another component's render (cache subscribers may fire
        // mid-render under Suspense).
        queueMicrotask(tryHydrate);
      }
    });
    return () => {
      unsubAuth();
      unsubCache();
    };
  }, [hydratedFromMe, qc]);

  useEffect(() => {
    const next = resolve(preference);
    setResolved(next);
    document.documentElement.setAttribute('data-theme', next);
    try {
      localStorage.setItem(storageKey, preference);
    } catch {
      // ignore
    }
    // Background-sync to the server when authenticated. Fire-and-forget;
    // the form-driven path uses the typed mutation hook and is the source
    // of truth for explicit saves.
    if (hydratedFromMe && authStore.getState().accessToken) {
      void sdk
        .PATCH('/me', { body: { themePreference: preference } })
        .then(({ data }) => {
          if (data) qc.setQueryData<Me>(['me'], data);
        })
        .catch(() => {
          // ignore — local state is the fast path
        });
    }
  }, [preference, hydratedFromMe, qc]);

  useEffect(() => {
    if (preference !== 'system') return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = (): void => {
      const next = resolveSystem();
      setResolved(next);
      document.documentElement.setAttribute('data-theme', next);
    };
    mq.addEventListener('change', onChange);
    return () => {
      mq.removeEventListener('change', onChange);
    };
  }, [preference]);

  const value: ThemeContextValue = {
    preference,
    resolved,
    setPreference: setPreferenceState,
  };

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

/** Access the current theme preference and setter. */
export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider');
  return ctx;
}
