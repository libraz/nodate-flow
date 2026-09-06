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
 *
 *   3. The snapshot is only consumed by a restoration that actually runs,
 *      and a restoration only runs if the trap is still deactivated when
 *      the microtask fires. Under StrictMode React mounts effects, tears
 *      them down, and mounts them again **without re-rendering**, so a
 *      cleanup that cleared the snapshot would destroy the only copy of
 *      the opener: the render-phase capture cannot re-run, and the real
 *      close later would have nothing to focus, leaving `<body>` active.
 *      Each setup takes an activation number; a cleanup restores focus
 *      only while its own activation is still the latest, which tells a
 *      genuine close apart from a remount that immediately re-armed the
 *      trap.
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
  // The ref is cleared by the restoration itself, so the next activation
  // captures fresh.
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  if (active && previouslyFocusedRef.current === null && typeof document !== 'undefined') {
    previouslyFocusedRef.current = document.activeElement as HTMLElement | null;
  }

  // Incremented by every effect setup. A cleanup owns the snapshot only
  // while its activation is still current.
  const activationRef = useRef(0);

  useLayoutEffect(() => {
    if (!active) return;
    const container = containerRef.current;
    if (!container) return;

    activationRef.current += 1;
    const activation = activationRef.current;

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
      if (!toFocus) return;
      const restore = (): void => {
        // A later setup has re-armed the trap since this cleanup ran, so
        // this was a remount rather than a close: the trap owns focus and
        // the snapshot still belongs to whoever opened it.
        if (activationRef.current !== activation) return;
        previouslyFocusedRef.current = null;
        toFocus.focus?.();
      };
      // Defer to a microtask so any sibling layout-effect cleanups
      // (notably `useOverlayLock`, which removes `inert` from the
      // opener's ancestor) finish first. Otherwise focus() lands on an
      // element that the browser still considers inert and silently
      // redirects to `<body>`.
      if (typeof queueMicrotask === 'function') {
        queueMicrotask(restore);
      } else {
        restore();
      }
    };
  }, [active, containerRef]);
}
