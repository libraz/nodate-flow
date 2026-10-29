/**
 * usePresenceList — keeps a removed list item rendered long enough to
 * play its exit.
 *
 * The list renders `entries` instead of `items`. An item that disappears
 * from `items` stays in `entries` with `present: false`, at the position
 * it occupied, until its {@link PresenceItem} calls `onExited`. The data
 * is never held back: the query cache has already moved on, only the
 * DOM lingers for one exit animation. An item that comes back mid-exit
 * (a rolled-back optimistic removal) is simply present again.
 *
 * Membership is compared by key, not by array identity, so a caller may
 * pass a freshly filtered array on every render.
 */

import { useState } from 'react';

export interface PresenceEntry<T> {
  key: string;
  item: T;
  present: boolean;
}

interface PresenceState<T> {
  signature: string;
  entries: PresenceEntry<T>[];
}

function merge<T>(
  previous: readonly PresenceEntry<T>[],
  items: readonly T[],
  getKey: (item: T) => string,
): PresenceEntry<T>[] {
  const next = items.map((item) => ({ key: getKey(item), item, present: true }));
  const nextKeys = new Set(next.map((entry) => entry.key));
  previous.forEach((entry, index) => {
    if (nextKeys.has(entry.key)) return;
    // Re-insert after the closest earlier entry that is still rendered.
    let at = 0;
    for (let j = index - 1; j >= 0; j -= 1) {
      const anchorKey = previous[j]?.key;
      const position = next.findIndex((candidate) => candidate.key === anchorKey);
      if (position >= 0) {
        at = position + 1;
        break;
      }
    }
    next.splice(at, 0, { ...entry, present: false });
  });
  return next;
}

export function usePresenceList<T>(
  items: readonly T[],
  getKey: (item: T) => string,
): { entries: PresenceEntry<T>[]; onExited: (key: string) => void } {
  const signature = JSON.stringify(items.map(getKey));
  const [state, setState] = useState<PresenceState<T>>(() => ({
    signature,
    entries: items.map((item) => ({ key: getKey(item), item, present: true })),
  }));

  // Derived-state update during render: the merge lands before commit.
  let entries = state.entries;
  if (state.signature !== signature) {
    entries = merge(state.entries, items, getKey);
    setState({ signature, entries });
  }

  // Same membership, newer data: present entries always render the latest item.
  const latest = new Map(items.map((item) => [getKey(item), item]));
  const rendered = entries.map((entry) => {
    const item = entry.present ? latest.get(entry.key) : undefined;
    return item === undefined ? entry : { ...entry, item };
  });

  const onExited = (key: string): void => {
    setState((prev) => ({
      ...prev,
      entries: prev.entries.filter((entry) => entry.present || entry.key !== key),
    }));
  };

  return { entries: rendered, onExited };
}
