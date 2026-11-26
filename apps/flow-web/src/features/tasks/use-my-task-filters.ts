/**
 * useMyTaskFilters — shared filter state and sort for /today.
 *
 * Filters live in memory only (session-local, like `useTaskFilters`); the
 * sort is a display preference and persists in `localStorage`, read the
 * way `useTaskView` reads its mode.
 */

import { useSyncExternalStore } from 'react';

import { MY_TASK_SORTS, type MyTaskFilters, type MyTaskSort } from './api';

type Filters = Required<MyTaskFilters>;

export const DEFAULT_MY_TASK_FILTERS: Filters = Object.freeze({
  search: '',
  states: Object.freeze(['open', 'waiting', 'review'] as const),
  priority: Object.freeze([]),
  workspaceId: '',
  roles: Object.freeze([]),
}) as Filters;

let current: Filters = DEFAULT_MY_TASK_FILTERS;
const filterListeners = new Set<() => void>();

function subscribeFilters(listener: () => void): () => void {
  filterListeners.add(listener);
  return () => {
    filterListeners.delete(listener);
  };
}

export function getMyTaskFilters(): Filters {
  return current;
}

export function setMyTaskFilters(next: Filters): void {
  current = next;
  for (const l of filterListeners) l();
}

export function resetMyTaskFilters(): void {
  setMyTaskFilters(DEFAULT_MY_TASK_FILTERS);
}

export function useMyTaskFilters(): Filters {
  return useSyncExternalStore(
    subscribeFilters,
    () => current,
    () => DEFAULT_MY_TASK_FILTERS,
  );
}

/** Whether two state lists hold the same states, ignoring order. */
export function sameStates(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((s) => b.includes(s));
}

/** True when nothing differs from {@link DEFAULT_MY_TASK_FILTERS}. */
export function isDefaultMyTaskFilters(filters: Filters): boolean {
  return (
    filters.search.trim() === '' &&
    sameStates(filters.states, DEFAULT_MY_TASK_FILTERS.states) &&
    filters.priority.length === 0 &&
    filters.workspaceId === '' &&
    filters.roles.length === 0
  );
}

const SORT_STORAGE_KEY = 'nf:my-tasks-sort';
const DEFAULT_SORT: MyTaskSort = 'dueOn';

const sortListeners = new Set<() => void>();

function readSort(): MyTaskSort {
  if (typeof window === 'undefined') return DEFAULT_SORT;
  try {
    const raw = window.localStorage.getItem(SORT_STORAGE_KEY);
    const known = MY_TASK_SORTS.find((s) => s === raw);
    if (known) return known;
  } catch {
    // ignore (private mode etc.)
  }
  return DEFAULT_SORT;
}

function subscribeSort(listener: () => void): () => void {
  sortListeners.add(listener);
  const onStorage = (e: StorageEvent): void => {
    if (e.key === SORT_STORAGE_KEY) listener();
  };
  window.addEventListener('storage', onStorage);
  return () => {
    sortListeners.delete(listener);
    window.removeEventListener('storage', onStorage);
  };
}

export function setMyTaskSort(sort: MyTaskSort): void {
  try {
    window.localStorage.setItem(SORT_STORAGE_KEY, sort);
  } catch {
    // ignore
  }
  for (const l of sortListeners) l();
}

export function useMyTaskSort(): MyTaskSort {
  return useSyncExternalStore(subscribeSort, readSort, () => DEFAULT_SORT);
}
