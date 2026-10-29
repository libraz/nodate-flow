/**
 * useSlidingIndicator — measures the selected item of a list so a single
 * absolutely-positioned element can slide between items (Tabs underline,
 * SegmentedControl thumb).
 *
 * The box is in the list's inline axis, with the offset converted to the
 * logical start edge so `inset-inline-start` works in either writing
 * direction. It is re-measured when the list or any item resizes and when
 * items are added, removed or relabelled. `null` means "no layout /
 * nothing selected"; callers fall back to the item's own styling.
 */

import { type RefObject, useLayoutEffect, useState } from 'react';

export interface IndicatorBox {
  start: number;
  size: number;
}

function measure(list: HTMLElement, item: HTMLElement): IndicatorBox | null {
  const size = item.offsetWidth;
  if (size === 0) return null;
  const rtl = getComputedStyle(list).direction === 'rtl';
  const start = rtl ? list.clientWidth - item.offsetLeft - size : item.offsetLeft;
  return { start, size };
}

export function useSlidingIndicator(
  listRef: RefObject<HTMLElement | null>,
  itemRefs: RefObject<Map<string, HTMLElement>>,
  activeKey: string | null,
): IndicatorBox | null {
  const [box, setBox] = useState<IndicatorBox | null>(null);
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const update = (): void => {
      const item = activeKey === null ? undefined : itemRefs.current.get(activeKey);
      const next = item ? measure(list, item) : null;
      setBox((prev) =>
        prev && next && prev.start === next.start && prev.size === next.size ? prev : next,
      );
    };
    update();
    // A sibling growing moves the active item without resizing it, so every item is observed.
    const resize = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update);
    const observeItems = (): void => {
      if (!resize) return;
      resize.observe(list);
      for (const node of itemRefs.current.values()) resize.observe(node);
    };
    observeItems();
    const mutation = new MutationObserver(() => {
      observeItems();
      update();
    });
    mutation.observe(list, { childList: true, subtree: true, characterData: true });
    return () => {
      resize?.disconnect();
      mutation.disconnect();
    };
  }, [listRef, itemRefs, activeKey]);
  return box;
}
