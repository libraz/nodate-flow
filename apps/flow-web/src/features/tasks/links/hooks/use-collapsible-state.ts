/**
 * useCollapsibleState — localStorage-backed collapse state for a
 * disclosure section.
 *
 * Reads the persisted value once on mount, then writes through to
 * `localStorage` whenever the caller flips the toggle. The default
 * `initialOpen` argument seeds the state when no persisted value
 * exists; subsequent reloads always honour the persisted choice.
 *
 * SSR-safe: the initial reader checks for `window` so the hook can be
 * imported into route entry files without crashing under non-DOM
 * environments (Vitest jsdom is fine; pure node modules would not be).
 */

import { useEffect, useState } from 'react';

const STORAGE_PREFIX = 'linkedEvents.collapsed.';

function readPersisted(key: string): boolean | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(`${STORAGE_PREFIX}${key}`);
    if (raw === '1') return true;
    if (raw === '0') return false;
    return null;
  } catch {
    return null;
  }
}

function writePersisted(key: string, collapsed: boolean): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(`${STORAGE_PREFIX}${key}`, collapsed ? '1' : '0');
  } catch {
    // ignore (private mode / quota)
  }
}

export interface CollapsibleState {
  collapsed: boolean;
  toggle: () => void;
  setCollapsed: (next: boolean) => void;
}

/**
 * @param key - Stable identifier; the same key is shared across reloads
 *              and tabs so the user's preference is global, not per-task.
 * @param initialCollapsed - Default state when no persisted value exists.
 */
export function useCollapsibleState(key: string, initialCollapsed: boolean): CollapsibleState {
  const [collapsed, setCollapsedState] = useState<boolean>(() => {
    const persisted = readPersisted(key);
    return persisted ?? initialCollapsed;
  });

  // Re-sync when the seeded default changes (n flips between 0 and >0).
  // The persisted value still wins; this only adjusts the unset case.
  useEffect(() => {
    const persisted = readPersisted(key);
    if (persisted === null) setCollapsedState(initialCollapsed);
  }, [key, initialCollapsed]);

  const setCollapsed = (next: boolean): void => {
    setCollapsedState(next);
    writePersisted(key, next);
  };
  const toggle = (): void => {
    setCollapsed(!collapsed);
  };
  return { collapsed, toggle, setCollapsed };
}
