/**
 * Pointer-driven drag for the calendar month grid.
 *
 * One implementation for every input device. The grid used the HTML5
 * drag-and-drop API — `draggable`, `dragstart`, `dataTransfer` — which
 * fires nothing at all for a finger, so moving a task or an event to
 * another day was a feature only a mouse could reach. Pointer events
 * describe mouse, trackpad, pen and touch as the same gesture, and the
 * drop target is resolved by hit-testing the registered day cells rather
 * than by the browser's own drop plumbing.
 *
 * A finger has to rest on the source for {@link TOUCH_HOLD_MS} before it
 * lifts. A month grid is a page of drop targets with nothing between
 * them, so without a deliberate start the first vertical swipe that
 * happened to begin on a pill would pick the pill up instead of
 * scrolling — and there would be no other way to scroll. A mouse has no
 * such conflict: it lifts as soon as the pointer has travelled far
 * enough not to swallow a click, which is immediate to a person moving
 * one.
 *
 * The floating copy is positioned by writing to its node directly, so a
 * gesture that emits a move event every frame does not re-render the
 * grid every frame. React state changes only when the drag begins, ends,
 * or crosses into another day.
 *
 * A drag held against the edge of a scrollable container moves it. On a
 * surface that scrolls — the phone month view scrolls through two years
 * of weeks — the days a drop can reach would otherwise be only the
 * handful already on screen, and the gesture refuses the native scroll
 * while it is lifted, so there is no other way to bring a day into view
 * without first putting the pill down. The container is the nearest
 * scrolling ancestor of the source, found once when the drag lifts; a
 * surface that scrolls with the page has none and simply does not
 * autoscroll.
 */

import {
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from 'react';

/**
 * How long a finger rests on a source before it lifts.
 *
 * The platform long-press is 500 ms on both iOS and Android, so a drag
 * that begins at the same moment the system's own press gestures do
 * feels like the device's rather than like a delay this grid invented.
 */
export const TOUCH_HOLD_MS = 500;

/** How far a mouse or pen travels before a press counts as a drag. */
const POINTER_SLOP_PX = 4;

/**
 * How far a finger may drift during the hold before the press is read as
 * a scroll and abandoned. Roughly the touch slop a browser itself uses to
 * separate a tap from a pan.
 */
const TOUCH_HOLD_SLOP_PX = 10;

/**
 * How far above the finger the floating copy rides. A fingertip covers
 * about this much of the screen, and the copy is the only feedback a
 * touch drag has.
 */
const TOUCH_LIFT_PX = 44;

/**
 * How close to a scrolling container's edge a lifted drag has to come
 * before the container starts moving. Wider than a fingertip, so the
 * band can be reached without the finger leaving the surface.
 */
const AUTOSCROLL_EDGE_PX = 64;

/** Fastest the container travels, per frame, at the very edge. */
const AUTOSCROLL_MAX_PX = 16;

/** Slowest it travels, so the shallow end of the band still moves. */
const AUTOSCROLL_MIN_PX = 2;

/**
 * The nearest ancestor of `el` that scrolls its own content.
 *
 * The document itself is not one: a page-scrolled surface (the desktop
 * month grid) has no edge to press a drag against, and treating the
 * viewport as a container would make every drag near the bottom of the
 * window scroll the whole page.
 */
function scrollableAncestor(el: HTMLElement): HTMLElement | null {
  let node = el.parentElement;
  while (node !== null && node !== document.body && node !== document.documentElement) {
    const overflowY = getComputedStyle(node).overflowY;
    if ((overflowY === 'auto' || overflowY === 'scroll') && node.scrollHeight > node.clientHeight) {
      return node;
    }
    node = node.parentElement;
  }
  return null;
}

/** Per-frame travel for a pointer `depth` px into the edge band. */
function autoScrollStep(depth: number): number {
  const ratio = Math.min(1, depth / AUTOSCROLL_EDGE_PX);
  return AUTOSCROLL_MIN_PX + ratio * (AUTOSCROLL_MAX_PX - AUTOSCROLL_MIN_PX);
}

/** What the grid needs to know about an in-flight drag while it renders. */
export interface PointerDragState<P> {
  /** What is being moved. */
  payload: P;
  /** Identifies the rendered source, so only that one draws as lifted. */
  sourceKey: string;
  /** `mouse` | `pen` | `touch` — a finger gets the raised copy. */
  pointerType: string;
  /** The day cell under the pointer, or null when it is outside the grid. */
  overKey: string | null;
}

/** A press that has not yet ended, lifted or not. */
interface Press<P> {
  pointerId: number;
  pointerType: string;
  sourceKey: string;
  payload: P;
  element: HTMLElement;
  /** Where the press began, for the movement thresholds. */
  startX: number;
  startY: number;
  /** Where inside the source the pointer took hold of it. */
  grabX: number;
  grabY: number;
  /** The source's width, so the floating copy keeps its proportions. */
  width: number;
  /** The latest pointer position, so the copy can be placed on mount. */
  lastX: number;
  lastY: number;
  holdTimer: ReturnType<typeof setTimeout> | null;
  lifted: boolean;
  overKey: string | null;
}

export interface PointerDragHandle<P> {
  /** Non-null from the moment the source lifts until the gesture ends. */
  drag: PointerDragState<P> | null;
  /** The source being held down but not yet lifted, for the press feedback. */
  holdingKey: string | null;
  /** Attach to the floating copy; the gesture positions it directly. */
  proxyRef: RefObject<HTMLDivElement | null>;
  /** `onPointerDown` for anything draggable. */
  pressSource: (e: ReactPointerEvent<HTMLElement>, sourceKey: string, payload: P) => void;
  /** `ref` for a day cell, registering it as a drop target. */
  dropCellRef: (cellKey: string) => (el: HTMLDivElement | null) => void;
  /**
   * True when the press that just ended was a drag. A drag ends with a
   * `pointerup` over the source's capture target, which the browser then
   * turns into a click — so without this the pill that was dragged also
   * opens.
   */
  wasDragged: () => boolean;
}

/** Place the floating copy under (or, for a finger, above) the pointer. */
function positionProxy(
  node: HTMLDivElement,
  pointerType: string,
  grabX: number,
  grabY: number,
  x: number,
  y: number,
): void {
  const lift = pointerType === 'touch' ? TOUCH_LIFT_PX : 0;
  node.style.transform = `translate3d(${Math.round(x - grabX)}px, ${Math.round(y - grabY - lift)}px, 0)`;
}

/**
 * usePointerDrag — a press-and-move gesture over a grid of day cells.
 *
 * `onDrop` receives the payload the press carried and the cell key it was
 * released over. It is not called when the release lands outside every
 * registered cell, nor when the gesture is cancelled by Escape or by the
 * browser taking the pointer away.
 */
export function usePointerDrag<P>(
  onDrop: (payload: P, cellKey: string) => void,
): PointerDragHandle<P> {
  const pressRef = useRef<Press<P> | null>(null);
  const cellsRef = useRef<Map<string, HTMLElement>>(new Map());
  const detachRef = useRef<(() => void) | null>(null);
  const draggedRef = useRef(false);
  const scrollerRef = useRef<HTMLElement | null>(null);
  const frameRef = useRef<number | null>(null);
  const proxyRef = useRef<HTMLDivElement | null>(null);
  const onDropRef = useRef(onDrop);
  const [drag, setDrag] = useState<PointerDragState<P> | null>(null);
  const [holdingKey, setHoldingKey] = useState<string | null>(null);

  // The window listeners a press installs outlive the render that
  // installed them, so they read the current handler through a ref
  // rather than the one that happened to be in scope.
  useEffect(() => {
    onDropRef.current = onDrop;
  });

  // A grid that unmounts mid-gesture must not leave its listeners on the
  // window; the source element is gone and every later move would be
  // measured against it.
  useEffect(
    () => () => {
      detachRef.current?.();
      detachRef.current = null;
      pressRef.current = null;
      if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
      scrollerRef.current = null;
    },
    [],
  );

  // The copy is rendered by the commit that starts the drag, so it does
  // not exist yet when the gesture lifts. Placing it here keeps it from
  // appearing at the origin for one frame before the next move arrives.
  useEffect(() => {
    const press = pressRef.current;
    const node = proxyRef.current;
    if (!drag || !press || !node) return;
    node.style.inlineSize = `${Math.round(press.width)}px`;
    positionProxy(node, press.pointerType, press.grabX, press.grabY, press.lastX, press.lastY);
  }, [drag]);

  /** Which registered day cell covers a viewport point. */
  function cellAt(x: number, y: number): string | null {
    for (const [key, el] of cellsRef.current) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      if (x >= r.left && x < r.right && y >= r.top && y < r.bottom) return key;
    }
    return null;
  }

  /** Re-read the day under the pointer, telling the grid only on a change. */
  function updateOver(press: Press<P>): void {
    const over = cellAt(press.lastX, press.lastY);
    if (over === press.overKey) return;
    press.overKey = over;
    setDrag((prev) => (prev === null ? prev : { ...prev, overKey: over }));
  }

  /**
   * Move the container while the drag rests near its edge.
   *
   * The pointer does not move during this, so the day under it changes
   * only because the content did — which is why the frame re-reads the
   * drop target rather than waiting for the next pointer event, one of
   * which may never come.
   */
  function startAutoScroll(press: Press<P>): void {
    if (frameRef.current !== null) return;
    const step = (): void => {
      frameRef.current = null;
      const scroller = scrollerRef.current;
      if (scroller === null || pressRef.current !== press || !press.lifted) return;
      const rect = scroller.getBoundingClientRect();
      const inReach =
        press.lastX >= rect.left - AUTOSCROLL_EDGE_PX &&
        press.lastX <= rect.right + AUTOSCROLL_EDGE_PX &&
        press.lastY >= rect.top - AUTOSCROLL_EDGE_PX &&
        press.lastY <= rect.bottom + AUTOSCROLL_EDGE_PX;
      if (inReach) {
        const fromTop = press.lastY - rect.top;
        const fromBottom = rect.bottom - press.lastY;
        let delta = 0;
        if (fromTop < AUTOSCROLL_EDGE_PX) delta = -autoScrollStep(AUTOSCROLL_EDGE_PX - fromTop);
        else if (fromBottom < AUTOSCROLL_EDGE_PX) {
          delta = autoScrollStep(AUTOSCROLL_EDGE_PX - fromBottom);
        }
        if (delta !== 0) {
          const before = scroller.scrollTop;
          scroller.scrollTop = before + delta;
          if (scroller.scrollTop !== before) updateOver(press);
        }
      }
      frameRef.current = requestAnimationFrame(step);
    };
    frameRef.current = requestAnimationFrame(step);
  }

  function stopAutoScroll(): void {
    if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
    frameRef.current = null;
    scrollerRef.current = null;
  }

  function endPress(): Press<P> | null {
    const press = pressRef.current;
    pressRef.current = null;
    detachRef.current?.();
    detachRef.current = null;
    stopAutoScroll();
    if (press?.holdTimer) clearTimeout(press.holdTimer);
    if (press?.lifted) {
      draggedRef.current = true;
      try {
        press.element.releasePointerCapture(press.pointerId);
      } catch {
        // The pointer may already be gone (cancel, or the source was
        // removed) — releasing a capture that no longer exists throws
        // and means the same thing as succeeding.
      }
    }
    setHoldingKey(null);
    setDrag(null);
    return press;
  }

  function pressSource(e: ReactPointerEvent<HTMLElement>, sourceKey: string, payload: P): void {
    // A second finger, or a right-click, is not a second drag.
    if (pressRef.current) return;
    if (e.pointerType === 'mouse' && e.button !== 0) return;

    const element = e.currentTarget;
    const rect = element.getBoundingClientRect();
    const press: Press<P> = {
      pointerId: e.pointerId,
      pointerType: e.pointerType,
      sourceKey,
      payload,
      element,
      startX: e.clientX,
      startY: e.clientY,
      grabX: e.clientX - rect.left,
      grabY: e.clientY - rect.top,
      width: rect.width,
      lastX: e.clientX,
      lastY: e.clientY,
      holdTimer: null,
      lifted: false,
      overKey: null,
    };
    pressRef.current = press;
    draggedRef.current = false;

    const lift = (): void => {
      if (press.lifted || pressRef.current !== press) return;
      press.lifted = true;
      press.holdTimer = null;
      press.overKey = cellAt(press.lastX, press.lastY);
      // Resolved from the source while it is still where the press found
      // it. A virtualized surface may recycle rows during the drag, and
      // the container itself is the one thing that outlives them.
      scrollerRef.current = scrollableAncestor(press.element);
      if (scrollerRef.current !== null) startAutoScroll(press);
      try {
        press.element.setPointerCapture(press.pointerId);
      } catch {
        // Capture is an optimisation here: the window listeners below
        // already see every move. A device that refuses it still drags.
      }
      // Nothing has scrolled yet — the finger was still through the hold
      // — so refusing the first touchmove keeps a scroll from ever
      // starting, which is the only moment it can be refused.
      if (press.pointerType === 'touch') {
        window.addEventListener('touchmove', block, { passive: false });
        window.addEventListener('contextmenu', block);
      }
      window.addEventListener('selectstart', block);
      setHoldingKey(null);
      setDrag({
        payload: press.payload,
        sourceKey: press.sourceKey,
        pointerType: press.pointerType,
        overKey: press.overKey,
      });
    };

    const move = (ev: PointerEvent): void => {
      if (ev.pointerId !== press.pointerId) return;
      press.lastX = ev.clientX;
      press.lastY = ev.clientY;
      const travelled = Math.hypot(ev.clientX - press.startX, ev.clientY - press.startY);
      if (!press.lifted) {
        if (press.pointerType === 'touch') {
          // The finger is panning the month, not picking anything up.
          if (travelled > TOUCH_HOLD_SLOP_PX) endPress();
          return;
        }
        if (travelled > POINTER_SLOP_PX) lift();
        return;
      }
      const node = proxyRef.current;
      if (node) {
        positionProxy(node, press.pointerType, press.grabX, press.grabY, ev.clientX, ev.clientY);
      }
      updateOver(press);
    };

    const up = (ev: PointerEvent): void => {
      if (ev.pointerId !== press.pointerId) return;
      const lifted = press.lifted;
      const target = press.overKey;
      endPress();
      // A release outside every day cell is how a drag is called off
      // without committing to anything.
      if (lifted && target !== null) onDropRef.current(press.payload, target);
    };

    const cancel = (ev: PointerEvent): void => {
      if (ev.pointerId !== press.pointerId) return;
      endPress();
    };

    const key = (ev: KeyboardEvent): void => {
      // Only a lifted source claims the key. A button merely held down
      // is not a drag, and swallowing Escape then would keep an overlay
      // open for a reason nobody could see.
      if (ev.key !== 'Escape' || !press.lifted) return;
      // Taken in the capture phase and stopped: an overlay listening for
      // Escape must not also close while a drag is being called off.
      ev.preventDefault();
      ev.stopPropagation();
      endPress();
    };

    function block(ev: Event): void {
      ev.preventDefault();
    }

    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
    window.addEventListener('pointercancel', cancel);
    window.addEventListener('keydown', key, true);
    detachRef.current = (): void => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      window.removeEventListener('pointercancel', cancel);
      window.removeEventListener('keydown', key, true);
      window.removeEventListener('touchmove', block);
      window.removeEventListener('contextmenu', block);
      window.removeEventListener('selectstart', block);
    };

    if (e.pointerType === 'touch') {
      setHoldingKey(sourceKey);
      press.holdTimer = setTimeout(lift, TOUCH_HOLD_MS);
    }
  }

  function dropCellRef(cellKey: string): (el: HTMLDivElement | null) => void {
    return (el) => {
      if (el === null) cellsRef.current.delete(cellKey);
      else cellsRef.current.set(cellKey, el);
    };
  }

  function wasDragged(): boolean {
    return draggedRef.current;
  }

  return { drag, holdingKey, proxyRef, pressSource, dropCellRef, wasDragged };
}
