/**
 * useTaskView — persisted "Board" vs "List" toggle for the tasks section.
 *
 * Backed by `localStorage` and `useSyncExternalStore` so multiple components
 * (the switcher + the index route) stay in sync without prop drilling and
 * without pulling a global store for a single boolean.
 */

import { useSyncExternalStore } from 'react';

export type TaskView = 'board' | 'list' | 'graph' | 'spreadsheet';

const STORAGE_KEY = 'nf:task-view';
const DEFAULT_VIEW: TaskView = 'board';

const listeners = new Set<() => void>();

function readView(): TaskView {
  if (typeof window === 'undefined') return DEFAULT_VIEW;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (raw === 'board' || raw === 'list' || raw === 'graph' || raw === 'spreadsheet') return raw;
  } catch {
    // ignore (private mode etc.)
  }
  return DEFAULT_VIEW;
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

function getServerSnapshot(): TaskView {
  return DEFAULT_VIEW;
}

export function setTaskView(view: TaskView): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, view);
  } catch {
    // ignore
  }
  for (const l of listeners) l();
}

export function useTaskView(): TaskView {
  return useSyncExternalStore(subscribe, readView, getServerSnapshot);
}
