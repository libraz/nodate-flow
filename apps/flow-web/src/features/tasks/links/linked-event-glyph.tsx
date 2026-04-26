/**
 * LinkedEventGlyph — hairline relation marker.
 *
 * Two glyphs share a single SVG vocabulary so they sit on the same
 * baseline at the same stroke weight:
 *
 * - `contributes_to` -> a forward arrow (→). The task feeds into the
 *   event ("you're preparing for it").
 * - `blocks` -> a barrier (⊣). The task gates the event ("nothing
 *   happens until I'm done").
 *
 * Both are decorative; the row pairs the glyph with a visually-hidden
 * `<span class="sr-only">` describing the kind so assistive technology
 * still reads the relation.
 */

import type { ReactElement } from 'react';

import styles from './linked-events.module.css';
import type { LinkKind } from './types';

export interface LinkedEventGlyphProps {
  kind: LinkKind;
  /** Optional override; defaults to the section's row glyph class. */
  className?: string;
}

export default function LinkedEventGlyph({ kind, className }: LinkedEventGlyphProps): ReactElement {
  const cls = className ?? styles.glyph;
  if (kind === 'blocks') {
    return (
      <svg
        className={cls}
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M3 8h8" />
        <path d="M11 4v8" />
      </svg>
    );
  }
  return (
    <svg
      className={cls}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M3 8h10" />
      <path d="M9 4l4 4-4 4" />
    </svg>
  );
}
