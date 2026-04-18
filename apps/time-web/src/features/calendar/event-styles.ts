import type { CSSProperties } from 'react';

import type { EventKind, ShowAs } from './types';

const STRIPE_GRADIENT =
  'repeating-linear-gradient(45deg, transparent, transparent 3px, rgba(255,255,255,0.4) 3px, rgba(255,255,255,0.4) 6px)';

const HATCH_GRADIENT =
  'repeating-linear-gradient(45deg, transparent, transparent 4px, rgba(0,0,0,0.08) 4px, rgba(0,0,0,0.08) 8px)';

/** Returns inline styles for an event based on its kind, showAs, and owner color. */
export function getEventStyle(kind: EventKind, showAs: ShowAs, color: string): CSSProperties {
  const style: CSSProperties = {};

  // Base kind styles
  switch (kind) {
    case 'event':
      style.backgroundColor = color;
      style.color = '#fff';
      break;
    case 'block':
      style.backgroundImage = STRIPE_GRADIENT;
      style.backgroundColor = color;
      style.color = '#fff';
      break;
    case 'free':
      style.backgroundColor = 'rgba(34, 197, 94, 0.1)';
      style.border = `1.5px dashed ${color}`;
      style.color = color;
      break;
  }

  // showAs modifiers
  switch (showAs) {
    case 'free':
      if (kind !== 'free') {
        style.backgroundColor = 'rgba(34, 197, 94, 0.15)';
        style.color = color;
      }
      break;
    case 'tentative':
      style.opacity = 0.6;
      if (kind !== 'free') {
        style.backgroundImage = HATCH_GRADIENT;
      }
      break;
    case 'oof':
      style.backgroundColor = '#9ca3af';
      style.color = '#fff';
      break;
    // 'busy' is default, no extra styling
  }

  return style;
}

/** CSS class names for the event pill container. */
export function getEventClassName(kind: EventKind): string {
  const base = 'w-full truncate rounded px-1 py-0.5 text-left text-[11px] leading-tight';
  if (kind === 'free') {
    return `${base} bg-transparent`;
  }
  return base;
}
