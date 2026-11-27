/**
 * useBoardCancelledOpen — remembers whether the board's Cancelled column is
 * expanded or collapsed into its rail. Same storage pattern as `useTaskView`.
 */

import { useSyncExternalStore } from 'react';

const STORAGE_KEY = 'nf:board-cancelled-open';

const listeners = new Set<() => void>();

function readOpen(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  const onStorage = (e: StorageEvent): void => {
    if (e.key === STORAGE_KEY) listener();
  };
  window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('storage', onStorage);
  };
}

export function setBoardCancelledOpen(open: boolean): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, open ? '1' : '0');
  } catch {
    // ignore (private mode etc.)
  }
  for (const l of listeners) l();
}

export function useBoardCancelledOpen(): boolean {
  return useSyncExternalStore(subscribe, readOpen, () => false);
}
