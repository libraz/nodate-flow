/**
 * useActiveItemInView — scrolls a sideways-scrolling list (Tabs, a
 * SegmentedControl on a narrow row) just far enough that the active item
 * is fully visible. Only the list itself scrolls, never the page, and only
 * when the active item changes, so it does not fight the user's own scroll.
 */

import { type RefObject, useLayoutEffect } from 'react';

export function useActiveItemInView(
  listRef: RefObject<HTMLElement | null>,
  itemRefs: RefObject<Map<string, HTMLElement>>,
  activeKey: string | null,
): void {
  useLayoutEffect(() => {
    const list = listRef.current;
    const item = activeKey === null ? undefined : itemRefs.current.get(activeKey);
    if (!list || !item || list.scrollWidth <= list.clientWidth) return;
    const bounds = list.getBoundingClientRect();
    const box = item.getBoundingClientRect();
    if (box.left < bounds.left) list.scrollLeft += box.left - bounds.left;
    else if (box.right > bounds.right) list.scrollLeft += box.right - bounds.right;
  }, [listRef, itemRefs, activeKey]);
}
