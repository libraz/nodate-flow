/**
 * usePresence — keeps an element mounted while its exit animation runs.
 *
 * `open` flipping to `false` does not unmount straight away: the element
 * re-renders with `state: 'closed'`, its CSS starts the exit animation off
 * `[data-state="closed"]`, and the hook unmounts on that element's
 * `animationend`. A timeout derived from the computed animation length
 * backs the event up, so a keyframe that never loads cannot strand the
 * element on screen.
 *
 * When the computed animation is absent or 0ms — reduced motion zeroes the
 * duration tokens, and test DOMs compute no styles at all — the unmount
 * happens in the same commit, so callers observe the synchronous behaviour
 * they had before any animation existed.
 *
 * The presence element runs one exit animation; children's animations are
 * ignored because `animationend` bubbles.
 *
 * @example
 * ```tsx
 * const presence = usePresence<HTMLDivElement>(open);
 * if (!presence.mounted) return null;
 * return (
 *   <div ref={presence.ref} data-state={presence.state} inert={!open}>…</div>
 * );
 * ```
 */

import { useCallback, useLayoutEffect, useRef, useState } from 'react';

export type PresenceState = 'open' | 'closed';

export interface Presence<T extends Element> {
  /** Whether the element should be rendered at all. */
  mounted: boolean;
  /** `'open'` while shown, `'closed'` during the exit animation. Render as `data-state`. */
  state: PresenceState;
  /** Attach to the element whose exit animation gates the unmount. */
  ref: (node: T | null) => void;
}

/** Slack past the computed animation end before the timeout forces the unmount. */
const FALLBACK_GRACE_MS = 50;

function parseTimeMs(value: string): number {
  const trimmed = value.trim();
  let ms = 0;
  if (trimmed.endsWith('ms')) ms = Number.parseFloat(trimmed);
  else if (trimmed.endsWith('s')) ms = Number.parseFloat(trimmed) * 1000;
  return Number.isFinite(ms) ? ms : 0;
}

/** Longest `delay + duration` across the element's computed animations, in ms. */
function computedAnimationMs(node: Element): number {
  if (typeof getComputedStyle !== 'function') return 0;
  const style = getComputedStyle(node);
  if (!style.animationName || style.animationName === 'none') return 0;
  const durations = style.animationDuration.split(',').map(parseTimeMs);
  const delays = style.animationDelay.split(',').map(parseTimeMs);
  let longest = 0;
  durations.forEach((duration, i) => {
    const delay = delays.length > 0 ? (delays[i % delays.length] ?? 0) : 0;
    longest = Math.max(longest, delay + duration);
  });
  return longest;
}

export function usePresence<T extends Element = HTMLElement>(open: boolean): Presence<T> {
  const [lingering, setLingering] = useState(open);
  // Derived-state update during render: reopening mid-exit re-arms at once.
  if (open && !lingering) setLingering(true);

  const nodeRef = useRef<T | null>(null);
  const ref = useCallback((node: T | null) => {
    nodeRef.current = node;
  }, []);

  useLayoutEffect(() => {
    if (open || !lingering) return;
    const node = nodeRef.current;
    const totalMs = node ? computedAnimationMs(node) : 0;
    if (!node || totalMs <= 0) {
      setLingering(false);
      return;
    }
    const finish = (): void => setLingering(false);
    const onEnd = (event: Event): void => {
      if (event.target === node) finish();
    };
    node.addEventListener('animationend', onEnd);
    const timer = setTimeout(finish, totalMs + FALLBACK_GRACE_MS);
    return () => {
      node.removeEventListener('animationend', onEnd);
      clearTimeout(timer);
    };
  }, [open, lingering]);

  return { mounted: open || lingering, state: open ? 'open' : 'closed', ref };
}
