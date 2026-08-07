/**
 * Overlay registry — body scroll lock, background `inert`, and Escape
 * routing for modal overlays.
 *
 * Shared between {@link Dialog} and {@link Drawer}. Every open overlay
 * holds one entry in a single module-level stack, which is also the
 * reference count: the first entry applies the lock and inerts background
 * siblings, the last removal restores them. One ledger, so the lock and
 * the Escape order can never disagree.
 *
 * Escape is dispatched to the top-most overlay only. Each overlay used to
 * register its own `document` keydown listener and call
 * `stopPropagation()`, which does nothing about sibling listeners on the
 * same node — propagation stops at the next node, not between listeners —
 * so every open overlay saw the same Escape and closed at once, taking
 * whatever was typed into the one underneath with it.
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

import { type RefObject, useLayoutEffect, useRef } from 'react';

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

/** One open overlay. Its position in {@link stack} is the reference count. */
interface OverlayEntry {
  /** Dismiss callback, or null when the overlay does not dismiss itself. */
  onEscape: (() => void) | null;
}

const stack: OverlayEntry[] = [];
let bodySnapshot: BodySnapshot | null = null;
let siblingSnapshots: SiblingSnapshot[] = [];

/**
 * Escape reaches at most one overlay: the last entry, which is the one
 * the user sees on top. Every overlay portals into the same host and
 * they share a z-index, so paint order is insertion order is
 * registration order. The event is stopped even when that overlay has no
 * dismiss callback, so a modal that opts out of Escape does not hand the
 * key to the overlay beneath it.
 *
 * Bubble phase on `document`, matching the per-overlay listeners this
 * replaced: a control inside the overlay (a combobox, a popover) still
 * gets to consume its own Escape before we see it.
 */
function onDocumentKeyDown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return;
  const top = stack[stack.length - 1];
  if (top === undefined) return;
  event.stopPropagation();
  top.onEscape?.();
}

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

  document.addEventListener('keydown', onDocumentKeyDown);

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

  document.removeEventListener('keydown', onDocumentKeyDown);

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
 * Join the overlay stack while `active` is true. Reference-counted: the
 * first entry applies the lock, the last removal restores it.
 *
 * `onEscape` is called only while this overlay is the top of the stack;
 * omit it to swallow Escape without dismissing.
 *
 * The `_containerRef` parameter is informational; the lock treats every
 * non-portal `<body>` child as background. It is reserved for future
 * per-overlay opt-out without changing the public hook signature.
 */
export function useOverlayLock(
  _containerRef: RefObject<HTMLElement | null>,
  active: boolean,
  onEscape?: () => void,
): void {
  // One entry per overlay for its whole lifetime, so re-rendering with a
  // new `onEscape` identity never reshuffles the stack.
  const entryRef = useRef<OverlayEntry | null>(null);
  entryRef.current ??= { onEscape: null };
  const entry = entryRef.current;

  // Declared before the registration effect so the entry carries a
  // current callback by the time it joins the stack: React runs layout
  // effects in declaration order.
  useLayoutEffect(() => {
    entry.onEscape = onEscape ?? null;
  }, [entry, onEscape]);

  useLayoutEffect(() => {
    if (!active) return;
    stack.push(entry);
    if (stack.length === 1) applyLock();
    return () => {
      const index = stack.indexOf(entry);
      if (index !== -1) stack.splice(index, 1);
      if (stack.length === 0) releaseLock();
    };
  }, [active, entry]);
}

/**
 * Test-only helper exposed for parity assertions. Returns the number of
 * open overlays without side effects. Not part of the public surface.
 */
export function getOverlayOpenCountForTests(): number {
  return stack.length;
}
