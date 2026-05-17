/**
 * useFocusTrap — confines Tab / Shift+Tab focus inside the supplied container
 * while `active` is true. On activation it focuses the first interactive element;
 * on deactivation it returns focus to the previously focused element.
 *
 * This is a low-level building block — modal / drawer primitives in F1b/c will
 * compose this together with a `<dialog>` element.
 *
 * Implementation notes — focus snapshot timing:
 *
 *   1. The "previously focused" element is captured during **render**, the
 *      first time `active` flips to `true`. This is critical when the trap
 *      is composed with `useOverlayLock`: the lock runs as a layout effect
 *      and stamps `inert` on every non-portal `<body>` child, which in real
 *      browsers synchronously redirects focus to `<body>`. Capturing in a
 *      layout effect would already see `<body>` (if invoked after the lock)
 *      or be sensitive to declaration order. Capturing in render-phase
 *      guarantees we see the real opener regardless.
 *
 *   2. Restoration of focus is **scheduled via `queueMicrotask`** at
 *      cleanup time. React fires `useLayoutEffect` cleanups in declaration
 *      order, which means the trap's cleanup runs *before* a sibling
 *      `useOverlayLock`'s cleanup — so calling `focus()` synchronously
 *      would target an element whose ancestor is still `inert` (and
 *      browsers silently redirect that to `<body>`). By deferring to a
 *      microtask, we let every sibling cleanup unwind first; the lock has
 *      removed `inert` from the opener's ancestor by the time the
 *      microtask runs, so focus actually lands on the trigger.
 */

import { type RefObject, useLayoutEffect, useRef } from 'react';

const FOCUSABLE_SELECTOR = [
  'a[href]',
  'area[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
  '[contenteditable="true"]',
].join(',');

export function useFocusTrap(containerRef: RefObject<HTMLElement | null>, active: boolean): void {
  // Render-phase capture. We snapshot the active element on the FIRST
  // render where `active` is true — before any layout effect (notably
  // `useOverlayLock`) gets a chance to mutate the DOM and move focus.
  // The ref is intentionally cleared in cleanup so the next activation
  // captures fresh.
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  if (active && previouslyFocusedRef.current === null && typeof document !== 'undefined') {
    previouslyFocusedRef.current = document.activeElement as HTMLElement | null;
  }

  useLayoutEffect(() => {
    if (!active) return;
    const container = containerRef.current;
    if (!container) return;

    const getFocusable = (): HTMLElement[] => {
      return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
        (el) => !el.hasAttribute('inert') && el.offsetParent !== null,
      );
    };

    const focusables = getFocusable();
    if (focusables.length > 0) {
      focusables[0]?.focus();
    } else {
      container.setAttribute('tabindex', '-1');
      container.focus();
    }

    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key !== 'Tab') return;
      const items = getFocusable();
      if (items.length === 0) {
        event.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      if (!first || !last) return;
      const activeEl = document.activeElement as HTMLElement | null;
      if (event.shiftKey) {
        if (activeEl === first || !container.contains(activeEl)) {
          event.preventDefault();
          last.focus();
        }
      } else if (activeEl === last) {
        event.preventDefault();
        first.focus();
      }
    };

    container.addEventListener('keydown', onKeyDown);
    return () => {
      container.removeEventListener('keydown', onKeyDown);
      const toFocus = previouslyFocusedRef.current;
      previouslyFocusedRef.current = null;
      // Defer to a microtask so any sibling layout-effect cleanups
      // (notably `useOverlayLock`, which removes `inert` from the
      // opener's ancestor) finish first. Otherwise focus() lands on an
      // element that the browser still considers inert and silently
      // redirects to `<body>`.
      if (toFocus && typeof queueMicrotask === 'function') {
        queueMicrotask(() => toFocus.focus?.());
      } else {
        toFocus?.focus?.();
      }
    };
  }, [active, containerRef]);
}
