/**
 * useTaskFilters — in-memory shared filter state for the tasks section.
 *
 * Scoped per project id so navigating between projects does not bleed
 * filters across boards. Not persisted: filters are session-local.
 */

import { useSyncExternalStore } from 'react';

import type { TaskDerivedState, TaskFilters } from './api';

type Store = Map<string, TaskFilters>;

const store: Store = new Map();
const listeners = new Set<() => void>();
const EMPTY: TaskFilters = Object.freeze({
  search: '',
  states: Object.freeze([]) as readonly TaskDerivedState[],
  assigneeId: '',
}) as TaskFilters;

function emit(): void {
  for (const l of listeners) l();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function emptyFilters(): TaskFilters {
  return EMPTY;
}

export function getTaskFilters(projectId: string): TaskFilters {
  return store.get(projectId) ?? EMPTY;
}

export function setTaskFilters(projectId: string, next: TaskFilters): void {
  store.set(projectId, next);
  emit();
}

export function setTaskFilterSearch(projectId: string, search: string): void {
  setTaskFilters(projectId, { ...getTaskFilters(projectId), search });
}

export function setTaskFilterStates(projectId: string, states: readonly TaskDerivedState[]): void {
  setTaskFilters(projectId, { ...getTaskFilters(projectId), states });
}

export function setTaskFilterAssignee(projectId: string, assigneeId: string): void {
  setTaskFilters(projectId, { ...getTaskFilters(projectId), assigneeId });
}

export function useTaskFilters(projectId: string): TaskFilters {
  return useSyncExternalStore(
    subscribe,
    () => getTaskFilters(projectId),
    () => emptyFilters(),
  );
}
