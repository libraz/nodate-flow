/**
 * stream-health — tiny singleton publishing whether the realtime SSE
 * stream is currently healthy.
 *
 * Feature hooks read `useStreamHealthy()` to decide whether they
 * should run their fallback `refetchInterval`. When the stream is
 * healthy, polling is disabled and invalidation is the only trigger
 * for refetches (ADR 0005).
 */

import { useSyncExternalStore } from 'react';

type Listener = (healthy: boolean) => void;

let healthy = true;
const listeners = new Set<Listener>();

/** Update the stream health flag. Called by `useWorkspaceStream`. */
export function setStreamHealthy(next: boolean): void {
  if (healthy === next) return;
  healthy = next;
  for (const listener of listeners) listener(next);
}

function subscribe(listener: () => void): () => void {
  const wrapped: Listener = () => listener();
  listeners.add(wrapped);
  return () => {
    listeners.delete(wrapped);
  };
}

function getSnapshot(): boolean {
  return healthy;
}

/**
 * useStreamHealthy — returns true while the realtime stream is
 * considered healthy. Hooks should disable `refetchInterval` when
 * this returns true.
 */
export function useStreamHealthy(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
