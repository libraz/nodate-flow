/**
 * VisuallyHidden — content remains accessible to assistive technology
 * but is removed from the visual layout. Implements the standard "sr-only"
 * clip pattern.
 *
 * There is no `focusable` escape hatch. It existed as a prop, documented
 * as "becomes visible on keyboard focus (useful for skip links)", and
 * set a `data-nf-focusable` attribute that nothing read — a skip link
 * built on it stayed invisible on focus, which is the one thing a skip
 * link has to do. SkipLink in this same directory does that properly, so
 * the prop was promising a second and worse way to reach a solved
 * problem. Reach for SkipLink.
 */

import type { CSSProperties, JSX, ReactNode } from 'react';

export interface VisuallyHiddenProps {
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

export default function VisuallyHidden({ children }: VisuallyHiddenProps): JSX.Element {
  return <span style={HIDDEN}>{children}</span>;
}
