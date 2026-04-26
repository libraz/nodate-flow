/**
 * useBulkSelection — selection set with shift-click range support.
 *
 * Keeps a `Set<string>` of selected ids plus the "anchor" of the last
 * non-shift toggle. When `toggle(id, { shift: true })` is invoked the
 * range from anchor → id (inclusive, in the current visible order) is
 * unioned into the set. When called without shift the anchor becomes
 * `id`. Callers must pass the current visible order via `orderedIds` so
 * the hook can resolve an anchor → target slice.
 */

import { useCallback, useMemo, useState } from 'react';

export interface UseBulkSelectionOptions {
  /** Visible row order, used to resolve shift-click ranges. */
  orderedIds: readonly string[];
}

export interface UseBulkSelectionResult {
  selected: ReadonlySet<string>;
  count: number;
  isSelected: (id: string) => boolean;
  toggle: (id: string, opts?: { shift?: boolean }) => void;
  clear: () => void;
  /** Replace the entire selection with the provided ids. */
  setMany: (ids: readonly string[]) => void;
}

export function useBulkSelection({ orderedIds }: UseBulkSelectionOptions): UseBulkSelectionResult {
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());
  const [anchor, setAnchor] = useState<string | null>(null);

  const indexById = useMemo(() => {
    const map = new Map<string, number>();
    for (let i = 0; i < orderedIds.length; i += 1) {
      const id = orderedIds[i];
      if (id) map.set(id, i);
    }
    return map;
  }, [orderedIds]);

  const isSelected = useCallback((id: string): boolean => selected.has(id), [selected]);

  const toggle = useCallback(
    (id: string, opts?: { shift?: boolean }) => {
      if (opts?.shift && anchor && anchor !== id) {
        const a = indexById.get(anchor);
        const b = indexById.get(id);
        if (a !== undefined && b !== undefined) {
          const lo = Math.min(a, b);
          const hi = Math.max(a, b);
          const next = new Set(selected);
          for (let i = lo; i <= hi; i += 1) {
            const rid = orderedIds[i];
            if (rid) next.add(rid);
          }
          setSelected(next);
          return;
        }
      }
      const next = new Set(selected);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      setSelected(next);
      setAnchor(id);
    },
    [anchor, indexById, orderedIds, selected],
  );

  const clear = useCallback(() => {
    setSelected(new Set());
    setAnchor(null);
  }, []);

  const setMany = useCallback((ids: readonly string[]) => {
    setSelected(new Set(ids));
    setAnchor(ids.length > 0 ? (ids[ids.length - 1] ?? null) : null);
  }, []);

  return { selected, count: selected.size, isSelected, toggle, clear, setMany };
}
