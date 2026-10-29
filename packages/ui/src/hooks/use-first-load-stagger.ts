/**
 * useFirstLoadStagger — staggers the entrance of the items a list paints
 * with, and only those.
 *
 * Returns a style factory for each item's `--stagger-index`. Once the
 * first-load cascade has finished the factory returns `undefined`, so a
 * row added later (a new task, a posted comment) enters immediately
 * rather than after a delay derived from wherever it landed.
 */

import { type CSSProperties, useEffect, useState } from 'react';

/** Items past this index enter together with the last staggered one. */
const STAGGER_LIMIT = 8;

/**
 * Long enough for the last staggered item (index 8 at a sixth of the
 * base duration each) to finish its 180ms entrance.
 */
const SETTLE_MS = 600;

export function useFirstLoadStagger(): (index: number) => CSSProperties | undefined {
  const [settled, setSettled] = useState(false);

  useEffect(() => {
    const timer = setTimeout(() => setSettled(true), SETTLE_MS);
    return () => clearTimeout(timer);
  }, []);

  return (index) =>
    settled ? undefined : ({ '--stagger-index': Math.min(index, STAGGER_LIMIT) } as CSSProperties);
}
