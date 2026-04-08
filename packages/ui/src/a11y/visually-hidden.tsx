/**
 * VisuallyHidden — content remains accessible to assistive technology
 * but is removed from the visual layout. Implements the standard "sr-only"
 * clip pattern.
 */

import type { CSSProperties, JSX, ReactNode } from 'react';

export interface VisuallyHiddenProps {
  children: ReactNode;
  /** When true, becomes visible on keyboard focus (useful for skip links). */
  focusable?: boolean;
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

export default function VisuallyHidden({ children, focusable }: VisuallyHiddenProps): JSX.Element {
  return (
    <span style={HIDDEN} data-nf-focusable={focusable ? '' : undefined}>
      {children}
    </span>
  );
}
