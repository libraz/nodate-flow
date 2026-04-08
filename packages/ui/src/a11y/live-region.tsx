/**
 * LiveRegion — wraps content in an ARIA live region for assistive-tech
 * announcements (toasts, async status messages, etc.).
 */

import type { CSSProperties, JSX, ReactNode } from 'react';

export type LiveRegionPoliteness = 'polite' | 'assertive';

export interface LiveRegionProps {
  /** ARIA live politeness. Defaults to `'polite'`. */
  politeness?: LiveRegionPoliteness;
  /** When true, the entire region content is announced on update. */
  atomic?: boolean;
  children: ReactNode;
}

const HIDDEN: CSSProperties = {
  position: 'absolute',
  inlineSize: '1px',
  blockSize: '1px',
  padding: 0,
  margin: '-1px',
  overflow: 'hidden',
  clip: 'rect(0, 0, 0, 0)',
  whiteSpace: 'nowrap',
  borderWidth: 0,
};

export default function LiveRegion({
  politeness = 'polite',
  atomic = true,
  children,
}: LiveRegionProps): JSX.Element {
  return (
    <output aria-live={politeness} aria-atomic={atomic} style={HIDDEN}>
      {children}
    </output>
  );
}
