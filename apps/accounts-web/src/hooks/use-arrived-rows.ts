/**
 * useArrivedRows — tells rows that appeared after a list first loaded
 * from rows it painted with, so only the former play an entrance.
 *
 * `ready` flips true once the first response has landed; the keys held
 * at that moment are the initial set.
 */

import { useState } from 'react';

export function useArrivedRows(keys: readonly string[], ready: boolean): (key: string) => boolean {
  const [initial, setInitial] = useState<ReadonlySet<string> | null>(null);
  if (initial === null && ready) setInitial(new Set(keys));
  return (key) => initial !== null && !initial.has(key);
}
