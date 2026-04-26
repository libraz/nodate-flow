/**
 * Local-only dismissal store for AI priority suggestions.
 *
 * The priority suggestions endpoint has no server-side acknowledgement
 * mechanism (no `apply` / `dismiss` POST), so we record dismissals in
 * `localStorage` keyed by workspace and filter the response client-side.
 *
 * Storage key: `aiPriority.dismissed.{workspaceId}` — JSON array of taskIds.
 */

import { useCallback, useSyncExternalStore } from 'react';

const STORAGE_PREFIX = 'aiPriority.dismissed.';

/** Build the localStorage key for a given workspace. */
function storageKey(workspaceId: string): string {
  return `${STORAGE_PREFIX}${workspaceId}`;
}

/** Read the dismissed taskId set from localStorage. Tolerates corrupt JSON. */
function readDismissed(workspaceId: string): readonly string[] {
  if (typeof window === 'undefined') return [];
  try {
    const raw = window.localStorage.getItem(storageKey(workspaceId));
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((v): v is string => typeof v === 'string');
  } catch {
    return [];
  }
}

/** Persist the dismissed list and notify subscribers. */
function writeDismissed(workspaceId: string, ids: readonly string[]): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(storageKey(workspaceId), JSON.stringify(ids));
  } catch {
    // Quota / private mode — fail silently. Worst case the user sees the
    // suggestion again next refresh.
  }
  // Notify same-window listeners; `storage` events only fire cross-window.
  window.dispatchEvent(new CustomEvent('nf:ai-priority-dismissed', { detail: { workspaceId } }));
}

/** Subscribe to changes from any window or our same-window custom event. */
function subscribe(workspaceId: string, listener: () => void): () => void {
  if (typeof window === 'undefined') return () => undefined;
  const onStorage = (e: StorageEvent): void => {
    if (e.key === storageKey(workspaceId)) listener();
  };
  const onCustom = (e: Event): void => {
    const detail = (e as CustomEvent<{ workspaceId: string }>).detail;
    if (detail?.workspaceId === workspaceId) listener();
  };
  window.addEventListener('storage', onStorage);
  window.addEventListener('nf:ai-priority-dismissed', onCustom);
  return () => {
    window.removeEventListener('storage', onStorage);
    window.removeEventListener('nf:ai-priority-dismissed', onCustom);
  };
}

/** Stable empty snapshot for SSR / pre-mount calls. */
const EMPTY: readonly string[] = Object.freeze([]);

/**
 * Snapshot cache so `useSyncExternalStore` returns referentially-stable
 * arrays between renders. Without this, `getSnapshot` would allocate a
 * new array on every read and trigger an infinite loop.
 */
const SNAPSHOTS = new Map<string, readonly string[]>();

function getSnapshot(workspaceId: string): readonly string[] {
  const fresh = readDismissed(workspaceId);
  const cached = SNAPSHOTS.get(workspaceId);
  if (cached && cached.length === fresh.length && cached.every((v, i) => v === fresh[i])) {
    return cached;
  }
  SNAPSHOTS.set(workspaceId, fresh);
  return fresh;
}

/** Public API returned by {@link useDismissedSuggestions}. */
export interface DismissStore {
  /** Current dismissed taskIds for the workspace. */
  dismissed: readonly string[];
  /** Membership predicate. */
  isDismissed: (taskId: string) => boolean;
  /** Record a dismissal. No-op if already present. */
  dismiss: (taskId: string) => void;
  /** Reverse a previous dismissal (used by Undo toasts). */
  undismiss: (taskId: string) => void;
}

/**
 * useDismissedSuggestions — reactive read of the per-workspace dismiss list
 * with imperative `dismiss` / `undismiss` actions. Backed by `localStorage`
 * and synced across same-window callers via a custom event.
 */
export function useDismissedSuggestions(workspaceId: string): DismissStore {
  const dismissed = useSyncExternalStore(
    (l) => subscribe(workspaceId, l),
    () => getSnapshot(workspaceId),
    () => EMPTY,
  );

  const dismiss = useCallback(
    (taskId: string): void => {
      const current = readDismissed(workspaceId);
      if (current.includes(taskId)) return;
      writeDismissed(workspaceId, [...current, taskId]);
    },
    [workspaceId],
  );

  const undismiss = useCallback(
    (taskId: string): void => {
      const current = readDismissed(workspaceId);
      if (!current.includes(taskId)) return;
      writeDismissed(
        workspaceId,
        current.filter((id) => id !== taskId),
      );
    },
    [workspaceId],
  );

  const isDismissed = useCallback(
    (taskId: string): boolean => dismissed.includes(taskId),
    [dismissed],
  );

  return { dismissed, isDismissed, dismiss, undismiss };
}
