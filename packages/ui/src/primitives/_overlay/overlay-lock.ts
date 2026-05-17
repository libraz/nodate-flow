/**
 * Overlay lock — body scroll lock + background `inert` for modal overlays.
 *
 * Shared between {@link Dialog} and {@link Drawer}. Reference-counted so
 * stacked overlays cooperate: the first opener applies the lock and inerts
 * background siblings; the last closer restores them.
 *
 * Background = every direct child of `<body>` that is NOT the portal host
 * (`#nf-portal-root`). The portal host itself stays interactive so the
 * top-most overlay (and any nested overlays sharing the host) remains
 * focusable and visible to assistive tech.
 *
 * Restoration is safe under concurrent state changes: every attribute we
 * mutate is recorded in a per-element snapshot before being changed and
 * replayed on release. We never touch elements that already had `inert`
 * or `aria-hidden` set by user code unless we recorded them ourselves.
 */

import { type RefObject, useLayoutEffect } from 'react';

const PORTAL_ID = 'nf-portal-root';
const LOCK_ATTR = 'data-nf-overlay-lock';
const MARK_ATTR = 'data-nf-bg-inert';

interface BodySnapshot {
  overflow: string | null;
  paddingInlineEnd: string | null;
  scrollbarGutter: string | null;
}

interface SiblingSnapshot {
  el: Element;
  hadInert: boolean;
  hadAriaHidden: string | null;
}

let openCount = 0;
let bodySnapshot: BodySnapshot | null = null;
let siblingSnapshots: SiblingSnapshot[] = [];

function getScrollbarWidth(): number {
  if (typeof window === 'undefined') return 0;
  return Math.max(0, window.innerWidth - document.documentElement.clientWidth);
}

function applyLock(): void {
  if (typeof document === 'undefined') return;
  const body = document.body;
  const style = body.style;

  bodySnapshot = {
    overflow: style.getPropertyValue('overflow') || null,
    paddingInlineEnd: style.getPropertyValue('padding-inline-end') || null,
    scrollbarGutter: style.getPropertyValue('scrollbar-gutter') || null,
  };

  const sbw = getScrollbarWidth();
  style.setProperty('overflow', 'hidden');
  if (sbw > 0) {
    style.setProperty('padding-inline-end', `${sbw}px`);
  } else {
    style.setProperty('scrollbar-gutter', 'stable');
  }
  body.setAttribute(LOCK_ATTR, '');

  siblingSnapshots = [];
  const children = Array.from(body.children);
  for (const child of children) {
    if (child.id === PORTAL_ID) continue;
    siblingSnapshots.push({
      el: child,
      hadInert: child.hasAttribute('inert'),
      hadAriaHidden: child.getAttribute('aria-hidden'),
    });
    child.setAttribute('inert', '');
    child.setAttribute('aria-hidden', 'true');
    child.setAttribute(MARK_ATTR, '');
  }
}

function releaseLock(): void {
  if (typeof document === 'undefined') return;
  const body = document.body;
  const style = body.style;

  if (bodySnapshot) {
    if (bodySnapshot.overflow === null) style.removeProperty('overflow');
    else style.setProperty('overflow', bodySnapshot.overflow);

    if (bodySnapshot.paddingInlineEnd === null) style.removeProperty('padding-inline-end');
    else style.setProperty('padding-inline-end', bodySnapshot.paddingInlineEnd);

    if (bodySnapshot.scrollbarGutter === null) style.removeProperty('scrollbar-gutter');
    else style.setProperty('scrollbar-gutter', bodySnapshot.scrollbarGutter);
  }
  body.removeAttribute(LOCK_ATTR);
  bodySnapshot = null;

  for (const snap of siblingSnapshots) {
    if (!snap.hadInert) snap.el.removeAttribute('inert');
    if (snap.hadAriaHidden === null) snap.el.removeAttribute('aria-hidden');
    else snap.el.setAttribute('aria-hidden', snap.hadAriaHidden);
    snap.el.removeAttribute(MARK_ATTR);
  }
  siblingSnapshots = [];
}

/**
 * Acquire the overlay lock while `active` is true. Reference-counted: the
 * first active call applies the lock; the last release restores it.
 *
 * The `_containerRef` parameter is informational; the lock currently treats
 * every non-portal `<body>` child as background. It is reserved for future
 * per-overlay opt-out without changing the public hook signature.
 */
export function useOverlayLock(
  _containerRef: RefObject<HTMLElement | null>,
  active: boolean,
): void {
  useLayoutEffect(() => {
    if (!active) return;
    openCount += 1;
    if (openCount === 1) applyLock();
    return () => {
      openCount -= 1;
      if (openCount === 0) releaseLock();
    };
  }, [active]);
}

/**
 * Test-only helper exposed for parity assertions. Returns the current
 * open count without side effects. Not part of the public surface.
 */
export function getOverlayOpenCountForTests(): number {
  return openCount;
}
