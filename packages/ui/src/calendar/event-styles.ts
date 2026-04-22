import type { CSSProperties } from 'react';

import type { EventKind, ShowAs } from './types';

const STRIPE_GRADIENT =
  'repeating-linear-gradient(45deg, transparent, transparent 3px, var(--nf-color-fg-on-accent, #fff) 3px, var(--nf-color-fg-on-accent, #fff) 6px)';

const HATCH_GRADIENT =
  'repeating-linear-gradient(45deg, transparent, transparent 4px, var(--nf-color-border, rgba(0,0,0,0.08)) 4px, var(--nf-color-border, rgba(0,0,0,0.08)) 8px)';

/** Returns inline styles for an event based on its kind, showAs, and owner color. */
export function getEventStyle(kind: EventKind, showAs: ShowAs, color: string): CSSProperties {
  const style: CSSProperties = {};

  switch (kind) {
    case 'event':
    case 'milestone':
      style.backgroundColor = color;
      style.color = 'var(--nf-color-fg-on-accent)';
      break;
    case 'block':
      style.backgroundImage = STRIPE_GRADIENT;
      style.backgroundColor = color;
      style.color = 'var(--nf-color-fg-on-accent)';
      break;
    case 'free':
      style.backgroundColor = 'var(--nf-color-success-subtle)';
      style.border = `1.5px dashed ${color}`;
      style.color = color;
      break;
  }

  switch (showAs) {
    case 'free':
      if (kind !== 'free') {
        style.backgroundColor = 'var(--nf-color-success-subtle)';
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
      style.backgroundColor = 'var(--nf-color-fg-muted)';
      style.color = 'var(--nf-color-fg-on-accent)';
      break;
    // 'busy' is the default — no additional styling.
  }

  return style;
}
