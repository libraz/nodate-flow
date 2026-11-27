/**
 * Collects query-key prefixes and flushes them as one batch: a trailing
 * window that restarts on every add, bounded by a max wait so a steady
 * stream still flushes.
 */

export type QueryKeyPrefix = readonly unknown[];

export interface InvalidationCoalescer {
  add(keys: Iterable<QueryKeyPrefix>): void;
  dispose(): void;
}

export interface CoalescerOptions {
  flush: (keys: QueryKeyPrefix[]) => void;
  windowMs?: number;
  maxWaitMs?: number;
}

export function createInvalidationCoalescer({
  flush,
  windowMs = 750,
  maxWaitMs = 2_000,
}: CoalescerOptions): InvalidationCoalescer {
  const pending = new Map<string, QueryKeyPrefix>();
  let trailing: ReturnType<typeof setTimeout> | null = null;
  let maxTimer: ReturnType<typeof setTimeout> | null = null;

  const clearTimers = (): void => {
    if (trailing !== null) clearTimeout(trailing);
    if (maxTimer !== null) clearTimeout(maxTimer);
    trailing = null;
    maxTimer = null;
  };

  const run = (): void => {
    clearTimers();
    if (pending.size === 0) return;
    const keys = [...pending.values()];
    pending.clear();
    flush(keys);
  };

  return {
    add(keys) {
      let added = false;
      for (const key of keys) {
        pending.set(JSON.stringify(key), key);
        added = true;
      }
      if (!added) return;
      if (trailing !== null) clearTimeout(trailing);
      trailing = setTimeout(run, windowMs);
      maxTimer ??= setTimeout(run, maxWaitMs);
    },
    dispose() {
      clearTimers();
      pending.clear();
    },
  };
}
